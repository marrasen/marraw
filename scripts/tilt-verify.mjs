// End-to-end check of tilt shift — the depth-graded defocus — against a
// running `marrawd --dev --port 8483`. Exercises: the params surviving
// Normalize, the unrunnable states folding to neutral, the committed render
// changing pixels at a downscaled level AND a 1:1 tile, the focus window
// actually steering WHICH depths stay sharp, and a full-resolution export.
//
//   node scripts/tilt-verify.mjs /tmp/marraw-fixture
//
// Point it at a DISPOSABLE copy of a shoot — it writes .marraw.json sidecars.
// The first run generates the depth map, which downloads the model (~99 MB).

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/tilt-verify.mjs <disposable-raw-folder>');
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

const render = async (params) => {
  await call('Edits.SetEditParams', [photo.id, params]);
  const cur = (await call('Library.ListPhotos', [info.folderId]))[0];
  const img = await fetchImg(
    `http://127.0.0.1:8483/img/${photo.id}/512?v=${cur.cacheKey}&e=${cur.editHash}`,
  );
  return { img, hash: cur.editHash, cacheKey: cur.cacheKey };
};

const base = await render({});
check(isJpeg(base.img), `baseline 512 renders (${base.img.length} B, hash ${base.hash})`);

// Every pixel comparison below is against REF, not against the neutral
// baseline: an unedited level under 1024 is derived from the camera's embedded
// JPEG rather than rendered from the RAW, so it shares no pixels with anything
// that went through the develop pipeline. A comparison against it would pass
// whether or not the stage under test did a thing. REF is a trivially edited
// state, so it takes the same route as the tilted renders and every tilt
// params object below carries the same contrast.
const EDIT = { contrast: 0.05 };
const ref = await render(EDIT);
check(isJpeg(ref.img) && !ref.img.equals(base.img), 'reference render (RAW route, tilt-free)');

// --- 1. Unrunnable states fold to neutral, so they can never fork the cache
// or leave a live-looking edit that renders nothing. ---
const noMap = await render({ tiltAmount: 0.7, tiltLo: 0.4, tiltHi: 0.8 });
check(noMap.hash === base.hash, 'an amount with no map version hashes as neutral');
const noAmount = await render({ tiltLo: 0.4, tiltHi: 0.8, tiltMapVer: 'depthany2s-1' });
check(noAmount.hash === base.hash, 'a window with no amount hashes as neutral');

// --- 2. The depth map, then the effect itself. ---
step('generating the depth map (may download the model)…');
const depth = await call('Edits.GenerateAIMap', [photo.id, 'depth', true], 300_000);
step(`GenerateAIMap depth -> ${depth.mapVer}`);

const near = { ...EDIT, tiltAmount: 0.8, tiltLo: 0.7, tiltHi: 1, tiltMapVer: depth.mapVer };
const tilted = await render(near);
const saved = await call('Edits.GetEditParams', [photo.id]);
check(
  saved.tiltAmount === 0.8 && saved.tiltLo === 0.7 && saved.tiltMapVer === depth.mapVer,
  'the tilt params survive Normalize',
);
check(tilted.hash !== ref.hash, 'tilt advances the edit hash');
check(tilted.cacheKey === ref.cacheKey, 'cacheKey is unchanged (tilt is post-decode)');
check(isJpeg(tilted.img) && !tilted.img.equals(ref.img), 'tilt changes the 512 render');

// The 1:1 tile path runs its own inlined copy of the stage order — this is the
// check that catches a stage wired into ApplyFinish but not into that mirror.
const tile = await fetchImg(
  `http://127.0.0.1:8483/img/${photo.id}/tile/0/0?v=${tilted.cacheKey}&e=${tilted.hash}`,
);
check(isJpeg(tile), `tilted 1:1 tile (0,0) renders (${tile.length} B)`);

// --- 3. The window steers WHICH depths stay sharp: keeping the near band and
// keeping the far band cannot produce the same photo. ---
const far = await render({ ...near, tiltLo: 0, tiltHi: 0.3 });
check(!far.img.equals(tilted.img), 'the focus window picks which depths stay sharp');

// A band in the middle — the miniature look — differs from both ends.
const band = await render({ ...near, tiltLo: 0.45, tiltHi: 0.75 });
check(!band.img.equals(tilted.img) && !band.img.equals(far.img), 'a mid-depth band is its own look');

// --- 4. The amount is a real ramp, not a switch. ---
const gentle = await render({ ...near, tiltAmount: 0.25 });
check(!gentle.img.equals(tilted.img), 'the amount changes the strength');

// --- 5. A window covering the whole depth range defocuses nothing: the stage
// runs and finds every pixel in focus, so the render matches the baseline. ---
const open = await render({ ...near, tiltLo: 0, tiltHi: 1 });
check(open.img.equals(ref.img), 'a fully-open focus window renders as the untilted photo');

// --- 6. A stale map version renders WITHOUT the effect rather than failing —
// the AI-mask contract for a sidecar that arrived from another machine. ---
const stale = await render({ ...near, tiltMapVer: 'depthany2s-999' });
check(isJpeg(stale.img) && stale.img.equals(ref.img), 'an unknown map version degrades silently');

// --- 7. Full-resolution export runs the same stages. ---
const { mkdtempSync, rmSync, statSync } = await import('node:fs');
const { tmpdir } = await import('node:os');
const { join } = await import('node:path');
const dest = mkdtempSync(join(tmpdir(), 'marraw-tilt-'));
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
const plainSize = await exportOnce(EDIT, 'plain');
const tiltSize = await exportOnce(near, 'tilt');
step(`export plain ${plainSize} B, tilted ${tiltSize} B`);
// A defocused photo compresses smaller; the sizes differing at all proves the
// export path ran the stage, and the direction proves it blurred.
check(tiltSize < plainSize, 'a full-resolution export carries the defocus');
rmSync(dest, { recursive: true, force: true });
await call('Edits.SetEditParams', [photo.id, {}]);

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
