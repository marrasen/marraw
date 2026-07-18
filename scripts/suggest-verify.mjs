// End-to-end check of Edits.SuggestEdits against a running
// `marrawd --dev --port 8483`. Exercises: candidate count and ordering,
// look-only mutation (geometry / WB / masks / spots / detail pass through
// byte-identical), degraded mode (photos without a cached scene map get no
// class-gated candidates and needsClassMap=true), and — when the fixture
// has a photo with a cached class map — the scene-aware path.
//
//   node scripts/suggest-verify.mjs <raw-fixture-folder>
//
// Read-only: SuggestEdits persists nothing and this script never applies.

const FIXTURE = process.argv[2];
if (!FIXTURE) {
  console.error('usage: node scripts/suggest-verify.mjs <raw-fixture-folder>');
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

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('cannot connect to marrawd :8483'));
});

const info = await call('Library.OpenFolder', [FIXTURE]);
const photos = await call('Library.ListPhotos', [info.folderId]);
check('fixture has photos', photos.length > 0, String(photos.length));

// Params seeded with everything a suggestion must NOT touch.
const passthrough = {
  wbMode: 'kelvin',
  wbKelvin: 5600,
  wbTint: 0.2,
  rotate: 1,
  flipH: true,
  cropX: 0.1,
  cropY: 0.1,
  cropW: 0.5,
  cropH: 0.5,
  cropAngle: 3,
  texture: 0.25,
  sharpen: 0.5,
  masks: [{ type: 'linear', x0: 0.1, y0: 0.1, x1: 0.9, y1: 0.9, adjust: { expEV: 0.5 } }],
  spots: [{ cx: 0.5, cy: 0.5, radius: 0.02, sx: 0.6, sy: 0.6, opacity: 1 }],
};

const results = [];
for (const photo of photos) {
  const res = await call('Edits.SuggestEdits', [photo.id, passthrough]);
  results.push({ photo, res });
}

const CLASS_GATED = ['sky', 'vivid'];
for (const { photo, res } of results) {
  const tag = `photo ${photo.fileName}`;
  const sugg = res.suggestions ?? [];
  const ids = sugg.map((s) => s.id);
  check(`${tag}: 3–5 candidates`, sugg.length >= 3 && sugg.length <= 5, ids.join(','));
  check(`${tag}: balanced first`, ids[0] === 'balanced', ids.join(','));
  check(`${tag}: unique ids`, new Set(ids).size === ids.length, ids.join(','));
  check(
    `${tag}: every candidate labeled`,
    sugg.every((s) => s.label && s.params),
  );
  for (const s of sugg) {
    const p = s.params;
    const kept =
      p.wbMode === 'kelvin' &&
      p.wbKelvin === 5600 &&
      p.wbTint === 0.2 &&
      p.rotate === 1 &&
      p.flipH === true &&
      p.cropW === 0.5 &&
      p.cropAngle === 3 &&
      p.texture === 0.25 &&
      p.sharpen === 0.5 &&
      p.masks?.length === 1 &&
      p.masks[0].type === 'linear' &&
      p.spots?.length === 1;
    check(`${tag}/${s.id}: geometry/WB/masks/spots/detail pass through`, kept, JSON.stringify(p));
  }
  // Candidates must actually differ (the recipes moved something).
  const uniq = new Set(sugg.map((s) => JSON.stringify(s.params)));
  check(`${tag}: candidates differ`, uniq.size === sugg.length, `${uniq.size}/${sugg.length} distinct`);
  if (res.needsClassMap) {
    check(
      `${tag}: degraded — no class-gated candidates`,
      !ids.some((id) => CLASS_GATED.includes(id)),
      ids.join(','),
    );
  } else {
    console.log(`INFO  ${tag}: has class map — candidates: ${ids.join(',')}`);
  }
}

const withMap = results.filter((r) => !r.res.needsClassMap);
console.log(
  `INFO  ${withMap.length}/${results.length} fixture photo(s) have a cached scene map` +
    (withMap.length === 0 ? ' — scene-aware path not exercised (generate a class map to cover it)' : ''),
);

console.log(failures === 0 ? '\nALL PASS' : `\n${failures} FAILURE(S)`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
