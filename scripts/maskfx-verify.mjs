// End-to-end check of the spatial mask effects (blur, motion, zoom, streaks,
// mosaic) against a running `marrawd --dev --port 8483`. Exercises: the FX
// fields surviving Normalize, the committed render pipeline changing pixels at
// a downscaled level AND a 1:1 tile, Invert, the smear direction, the
// inert-angle hash rule, and the headline path — an inverted AI subject mask
// defocused with light streaks — including through an export.
//
//   node scripts/maskfx-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/maskfx-verify.mjs <disposable-raw-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
const pushes = [];

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  } else if (msg.type === 'push') {
    pushes.push(msg);
  }
};

// Export completion arrives as a TaskStateEvent push, not as the RPC's reply.
const waitTask = async (taskId) => {
  const t = Date.now();
  while (Date.now() - t < 300_000) {
    for (const m of pushes) {
      if (m.event !== 'TaskStateEvent') continue;
      const task = m.data.tasks?.find((x) => x.id === taskId);
      if (task && (task.status === 'completed' || task.status === 'failed')) return task;
    }
    await new Promise((s) => setTimeout(s, 100));
  }
  throw new Error('export task timeout');
};

function call(method, params, timeoutMs = 60_000) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: ${method}`));
      }
    }, timeoutMs);
  });
}

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const step = (name) => console.log(name);

async function fetchImg(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`GET ${url} -> ${res.status}`);
  return Buffer.from(await res.arrayBuffer());
}
const isJpeg = (b) => b.length > 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff;

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
step(`OpenFolder -> ${photos.length} photos`);
if (photos.length < 1) throw new Error('need at least 1 RAW in the fixture');
const photo = photos[0];

// render commits the params and returns the 512 rendition plus the new hash.
const render = async (params) => {
  await call('Edits.SetEditParams', [photo.id, params]);
  const cur = (await call('Library.ListPhotos', [info.folderId]))[0];
  const img = await fetchImg(
    `http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`,
  );
  return { img, hash: cur.editHash, cacheKey: cur.cacheKey };
};

// Baseline (neutral) so every check below proves a real pixel change.
const base = await render({});
check(isJpeg(base.img), `baseline 512 renders (${base.img.length} B, hash ${base.hash})`);

// --- 1. A blur on a radial mask survives Normalize and advances the hash. ---
const radial = { type: 'radial', cx: 0.5, cy: 0.5, rx: 0.35, ry: 0.3, feather: 0.5 };
const blurMask = { ...radial, adjust: { blur: 0.6 } };
const blur = await render({ masks: [blurMask] });
const saved = await call('Edits.GetEditParams', [photo.id]);
check(saved.masks?.length === 1 && saved.masks[0].adjust?.blur === 0.6, 'blur survives Normalize');
check(blur.hash !== base.hash, 'a mask FX advances the edit hash');
check(blur.cacheKey === base.cacheKey, 'cacheKey is unchanged (FX is post-decode)');
check(isJpeg(blur.img) && !blur.img.equals(base.img), 'blur changes the 512 render vs baseline');

// The 1:1 tile path must run the FX too — this is the check that catches a
// stage wired into ApplyFinish but not into cache.generate's inlined mirror.
const tile = await fetchImg(
  `http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${blur.cacheKey}&e=${blur.hash}`,
);
check(isJpeg(tile), `blurred 1:1 tile (0,0) renders (${tile.length} B)`);

// --- 2. Invert defocuses the complement. ---
const inv = await render({ masks: [{ ...blurMask, invert: true }] });
check(!inv.img.equals(blur.img), 'invert defocuses the complement (different render)');

// --- 3. The smear direction is real end to end. ---
const motion0 = await render({ masks: [{ ...radial, adjust: { motionBlur: 0.5, fxAngle: 0 } }] });
const motion90 = await render({ masks: [{ ...radial, adjust: { motionBlur: 0.5, fxAngle: 90 } }] });
check(!motion0.img.equals(base.img), 'motion blur changes the render');
check(!motion0.img.equals(motion90.img), 'fxAngle steers the smear (0° ≠ 90°)');

// --- 4. An inert angle must round-trip to the baseline hash: nothing reads it
// when neither smear is live, so it must not fork the cache or un-neutral the
// mask. ---
const inert = await render({ masks: [{ ...radial, adjust: { fxAngle: 120 } }] });
const plain = await render({ masks: [{ ...radial, adjust: {} }] });
check(inert.hash === plain.hash, 'an inert fxAngle does not change the edit hash');

// --- 5. The remaining effects each render and each differ. ---
for (const [name, adjust] of [
  ['zoom blur', { zoomBlur: 0.6 }],
  ['mosaic', { mosaic: 0.5 }],
  ['streaks', { streaks: 0.6 }],
  ['glow', { glow: 0.7 }],
  ['prism', { prism: 0.8 }],
]) {
  const r = await render({ masks: [{ ...radial, adjust }] });
  check(isJpeg(r.img) && !r.img.equals(base.img), `${name} changes the render`);
}

// Prism is signed — the two directions must not collapse to the same render.
const prismPos = await render({ masks: [{ ...radial, adjust: { prism: 0.8 } }] });
const prismNeg = await render({ masks: [{ ...radial, adjust: { prism: -0.8 } }] });
check(!prismPos.img.equals(prismNeg.img), 'prism sign flips which channel goes outward');

// --- 6. The headline path: an inverted AI subject mask, defocused with
// streaks — the Background button's recipe. ---
step('generating the subject matte (may download the model)…');
const sub = await call('Edits.GenerateAIMap', [photo.id, 'subject', true], 300_000);
step(`GenerateAIMap subject -> ${sub.mapVer}`);
const background = {
  type: 'ai', aiKind: 'subject', mapVer: sub.mapVer, invert: true,
  adjust: { blur: 0.45, streaks: 0.35 },
};
const bg = await render({ masks: [background] });
check(isJpeg(bg.img) && !bg.img.equals(base.img), 'the background mask defocuses the render');
const subjectSide = await render({ masks: [{ ...background, invert: false }] });
check(!bg.img.equals(subjectSide.img), 'defocusing the background ≠ defocusing the subject');

// --- 7. Full-resolution export runs the same stages — the check that catches
// a stage that only ever ran on the downscaled preview path. ---
const { mkdtempSync, rmSync, statSync } = await import('node:fs');
const { tmpdir } = await import('node:os');
const { join } = await import('node:path');
const dest = mkdtempSync(join(tmpdir(), 'marraw-maskfx-'));
const exportOnce = async (params, name) => {
  await call('Edits.SetEditParams', [photo.id, params]);
  const ref = await call(
    'Export.StartExport',
    [{ photoIds: [photo.id], destDir: dest, format: 'jpeg', fileNameTemplate: name }],
    300_000,
  );
  const task = await waitTask(ref.taskId);
  if (task.status !== 'completed') throw new Error(`export ${name} ${task.status}`);
  return statSync(join(dest, `${name}.jpg`)).size;
};
const plainSize = await exportOnce({}, 'plain');
const fxSize = await exportOnce({ masks: [background] }, 'fx');
step(`export plain ${plainSize} B, background FX ${fxSize} B`);
check(plainSize !== fxSize, 'a full-resolution export carries the mask FX');
rmSync(dest, { recursive: true, force: true });
await call('Edits.SetEditParams', [photo.id, {}]);

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
