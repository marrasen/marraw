// End-to-end check of the preset system overhaul against a running
// `marrawd --dev --port 8483`. Exercises: the UserPreset schema round-trip
// (sections / relative / baseExpEV / autoSections / AI-mask recipes),
// setter validation, Settings.SetDefaultPresets + Library.ListCameras, and
// the per-camera default-preset seeding path — a fresh folder's photos come
// out of calibrate with the default look persisted as a real edit and the
// exposure re-anchored to each photo's measured baseline; reset returns to
// camera neutral.
//
//   node scripts/presets-verify.mjs <raw-fixture-folder>
//
// The fixture folder is only READ — the script copies one RAW into a
// disposable temp folder for the seeding pass (the copy gets a sidecar
// written next to it) and removes it afterwards.

import { copyFileSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FIXTURE = process.argv[2];
if (!FIXTURE) {
  console.error('usage: node scripts/presets-verify.mjs <raw-fixture-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  }
};

function call(method, params) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: ${method}`));
      }
    }, 120_000);
  });
}

let failures = 0;
function check(name, ok, detail = '') {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok || !detail ? '' : `  (${detail})`}`);
  if (!ok) failures++;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('cannot connect to marrawd :8483'));
});

const before = await call('Settings.GetUISettings', []);
const keepPresets = before.userPresets ?? [];
const keepDefaults = before.defaultPresets ?? {};

// ---------------------------------------------------------- schema round-trip

const testPreset = {
  id: 'verify-preset-1',
  name: 'verify tone+color',
  params: {
    expEV: 1.5,
    contrast: 0.3,
    saturation: 0.2,
    clarity: 0.15, // presence — excluded by sections below
    masks: [
      { type: 'ai', aiKind: 'subject', mapVer: '', threshold: 0.4, feather: 0.1, adjust: { expEV: 0.3 } },
    ],
  },
  sections: ['tone', 'color'],
  relative: false,
  baseExpEV: 1.0,
  autoSections: [],
};

await call('Settings.SetUserPresets', [[...keepPresets, testPreset]]);
const s1 = await call('Settings.GetUISettings', []);
const rt = (s1.userPresets ?? []).find((p) => p.id === 'verify-preset-1');
check('user preset round-trips', !!rt);
check('sections survive', JSON.stringify(rt?.sections) === '["tone","color"]', JSON.stringify(rt?.sections));
check('baseExpEV survives', rt?.baseExpEV === 1.0, String(rt?.baseExpEV));
check('params survive re-marshal', rt?.params?.contrast === 0.3 && rt?.params?.expEV === 1.5);
check(
  'AI mask recipe survives (mapVer empty)',
  rt?.params?.masks?.length === 1 &&
    rt.params.masks[0].aiKind === 'subject' &&
    !rt.params.masks[0].mapVer &&
    rt.params.masks[0].adjust?.expEV === 0.3,
  JSON.stringify(rt?.params?.masks),
);

let rejected = false;
try {
  await call('Settings.SetUserPresets', [[{ id: '', name: 'x', params: {} }]]);
} catch {
  rejected = true;
}
check('preset without id rejected', rejected);

// ------------------------------------------------------------ default presets

rejected = false;
try {
  await call('Settings.SetDefaultPresets', [{ ' ': 'some-id' }]);
} catch {
  rejected = true;
}
check('blank camera key rejected', rejected);

await call('Settings.SetDefaultPresets', [{ '*': 'verify-preset-1' }]);
const s2 = await call('Settings.GetUISettings', []);
check('default preset map round-trips', s2.defaultPresets?.['*'] === 'verify-preset-1');

// -------------------------------------------------------------- seeding pass

const dir = mkdtempSync(join(tmpdir(), 'marraw-presets-verify-'));
const raw = readdirSync(FIXTURE).find((f) => /\.(arw|cr2|cr3|nef|raf|dng)$/i.test(f));
if (!raw) throw new Error(`no RAW file in ${FIXTURE}`);
// Copy the RAW only — a sidecar would import its edit state and make the
// photo count as already-edited, which correctly suppresses seeding.
copyFileSync(join(FIXTURE, raw), join(dir, raw));

try {
  const info = await call('Library.OpenFolder', [dir]);
  // Calibrate is a background pass; poll until the photo carries both a
  // measured baseline and the seeded edit.
  let photo = null;
  for (let i = 0; i < 120; i++) {
    const photos = await call('Library.ListPhotos', [info.folderId]);
    photo = photos[0];
    if (photo && photo.baseExpEV !== 0 && photo.editHash && photo.editHash !== 'base') break;
    await sleep(1000);
  }
  check('photo calibrated (baseExpEV measured)', !!photo && photo.baseExpEV !== 0, JSON.stringify(photo?.baseExpEV));
  check('grid edit hash is non-base (seed is a real edit)', !!photo?.editHash && photo.editHash !== 'base', photo?.editHash);

  const seeded = await call('Edits.GetEditParams', [photo.id]);
  check('seeded contrast (tone section)', seeded?.contrast === 0.3, String(seeded?.contrast));
  check('seeded saturation (color section)', seeded?.saturation === 0.2, String(seeded?.saturation));
  check('presence section NOT seeded (partial preset)', (seeded?.clarity ?? 0) === 0, String(seeded?.clarity));
  // Preset saved at dial +1.5 over a +1.0 baseline → creative +0.5, re-anchored
  // onto this photo's own measured baseline.
  const wantEV = photo.baseExpEV + 0.5;
  check(
    'exposure re-anchored to the photo baseline',
    Math.abs((seeded?.expEV ?? 0) - wantEV) < 1e-6,
    `got ${seeded?.expEV}, want ${wantEV} (base ${photo.baseExpEV})`,
  );

  await call('Edits.ResetEdits', [[photo.id]]);
  const cleared = await call('Edits.GetEditParams', [photo.id]);
  check(
    'reset returns camera neutral (baseline only, no preset)',
    Math.abs((cleared?.expEV ?? 0) - photo.baseExpEV) < 1e-6 && (cleared?.contrast ?? 0) === 0,
    JSON.stringify(cleared),
  );
} finally {
  // ------------------------------------------------------------- cleanup
  await call('Settings.SetDefaultPresets', [keepDefaults]);
  await call('Settings.SetUserPresets', [keepPresets]);
  rmSync(dir, { recursive: true, force: true });
}

console.log(failures === 0 ? '\nALL PASS' : `\n${failures} FAILURE(S)`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
