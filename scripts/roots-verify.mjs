// Wire probe for the curated-library backend: library roots CRUD, the
// Add-folder picker queries (ListDirRaws / CountRaws), recursive scan through
// OpenFolder, managed library folders (ListShoots), and the filesystem watcher.
// Needs `marrawd --dev --port 8483` and a RAW folder to borrow files from.
//
//   node scripts/roots-verify.mjs "D:\Photos\<raw folder>"

import { copyFileSync, mkdirSync, mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/roots-verify.mjs <raw-folder>');
  process.exit(1);
}

// The watcher waits 2s of quiescence, and only files older than 1s are
// ingested, so every watcher assertion needs headroom over both.
const WATCH_DEADLINE_MS = 20_000;

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
// A subscription keeps its id: the server re-sends a `response` frame on every
// refresh trigger. That is how a watcher push is observed.
const subs = new Map();
ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    const sub = subs.get(msg.id);
    if (sub) {
      sub.pushes.push(msg.result);
      return;
    }
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
    }, 60_000);
  });
}

/** Opens a subscription and returns a handle collecting every pushed payload. */
function subscribe(method, params) {
  const id = String(nextId++);
  const handle = { id, pushes: [] };
  subs.set(id, handle);
  ws.send(JSON.stringify({ type: 'subscribe', id, method, params }));
  return handle;
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** Polls until predicate returns a truthy value, or the deadline passes. */
async function until(predicate, ms = WATCH_DEADLINE_MS) {
  const deadline = Date.now() + ms;
  for (;;) {
    const got = await predicate();
    if (got) return got;
    if (Date.now() > deadline) return null;
    await sleep(400);
  }
}

const results = {};
const check = (name, ok, detail = '') => {
  results[name] = ok;
  console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` (${detail})` : ''}`);
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('cannot connect to marrawd :8483'));
});

// Build a nested fixture: root/IMG_A.ARW + root/sub/IMG_B.ARW + root/export/IMG_C.ARW
const raws = readdirSync(FOLDER).filter((f) => f.toLowerCase().endsWith('.arw'));
if (raws.length < 1) throw new Error('need at least one ARW in the source folder');
const root = mkdtempSync(join(tmpdir(), 'marraw-roots-'));
mkdirSync(join(root, 'sub'));
mkdirSync(join(root, 'export'));
copyFileSync(join(FOLDER, raws[0]), join(root, 'IMG_A.ARW'));
copyFileSync(join(FOLDER, raws[0]), join(root, 'sub', 'IMG_B.ARW'));
copyFileSync(join(FOLDER, raws[0]), join(root, 'export', 'IMG_C.ARW'));

const eq = (a, b) => a.toLowerCase() === b.toLowerCase();

try {
  // Roots CRUD round-trip.
  const before = await call('Library.GetLibraryRoots', []);
  const mine = { path: root, alias: 'Probe Shoot', includeSubfolders: true, photoCount: 0 };
  await call('Library.SetLibraryRoots', [[...before, mine]]);
  const after = await call('Library.GetLibraryRoots', []);
  const stored = after.find((r) => r.path.toLowerCase() === root.toLowerCase());
  check('rootsRoundTrip', !!stored && stored.alias === 'Probe Shoot' && stored.includeSubfolders);

  // Picker queries.
  const entries = await call('Library.ListDirRaws', [root]);
  const sub = entries.find((e) => e.name === 'sub');
  check('listDirRaws', entries.length === 2 && sub?.rawCount === 1, JSON.stringify(entries.map((e) => `${e.name}:${e.rawCount}`)));
  const flat = await call('Library.CountRaws', [[root], false]);
  const deep = await call('Library.CountRaws', [[root], true]);
  check('countRaws', flat.files === 1 && deep.files === 2, `flat=${flat.files} deep=${deep.files}`);

  // Recursive scan through OpenFolder (root has includeSubfolders=true);
  // the export/ noise folder must be skipped.
  const info = await call('Library.OpenFolder', [root]);
  check('recursiveOpen', info.photoCount === 2, `photoCount=${info.photoCount}`);
  const photos = await call('Library.ListPhotos', [info.folderId]);
  const names = photos.map((p) => p.fileName).sort();
  check(
    'nestedNames',
    names.length === 2 && names[0] === 'IMG_A.ARW' && names[1].replace('/', '\\') === 'sub\\IMG_B.ARW',
    JSON.stringify(names),
  );

  // Flip the root to flat and rescan: the nested photo drops out.
  await call('Library.SetLibraryRoots', [[...before, { ...mine, includeSubfolders: false }]]);
  const flatInfo = await call('Library.OpenFolder', [root]);
  check('flatRescan', flatInfo.photoCount === 1, `photoCount=${flatInfo.photoCount}`);

  // ---- managed library folder --------------------------------------------

  const parent = { path: root, alias: '', includeSubfolders: false, photoCount: 0, isParent: true };
  await call('Library.SetLibraryRoots', [[...before, parent]]);
  const withParent = await call('Library.GetLibraryRoots', []);
  const storedParent = withParent.find((r) => eq(r.path, root));
  check('parentRoundTrip', !!storedParent && storedParent.isParent === true);

  // The whole point of serving children on a separate RPC: a synthesized child
  // must never appear in the settable roots list, or the client's next reorder
  // would persist it as a real root.
  const shoots = await call('Library.ListShoots', [root]);
  const reordered = await call('Library.GetLibraryRoots', []);
  check(
    'noSyntheticChildren',
    reordered.length === withParent.length &&
      !reordered.some((r) => eq(r.path, join(root, 'sub'))),
    JSON.stringify(reordered.map((r) => r.path)),
  );

  const self = shoots.find((s) => s.isSelf);
  const sub2 = shoots.find((s) => s.name === 'sub');
  check(
    'listShoots',
    shoots.length === 2 && self?.photoCount === 1 && sub2?.photoCount === 1,
    JSON.stringify(shoots.map((s) => `${s.name}:${s.photoCount}${s.isSelf ? ':self' : ''}`)),
  );
  check('listShootsSkipsNoise', !shoots.some((s) => s.name === 'export'));

  // A child of a parent scans recursively without any stored root of its own.
  const subPath = join(root, 'sub');
  mkdirSync(join(subPath, 'deeper'));
  copyFileSync(join(FOLDER, raws[0]), join(subPath, 'deeper', 'IMG_D.ARW'));
  const subInfo = await call('Library.OpenFolder', [subPath]);
  check('childScansRecursively', subInfo.photoCount === 2, `photoCount=${subInfo.photoCount}`);

  // A child that is also a stored root renders in its own right, not twice.
  await call('Library.SetLibraryRoots', [
    [...before, parent, { path: subPath, alias: '', includeSubfolders: false, photoCount: 0, isParent: false }],
  ]);
  const deduped = await call('Library.ListShoots', [root]);
  check('listShootsDedupsStoredRoot', !deduped.some((s) => eq(s.path, subPath)));
  await call('Library.SetLibraryRoots', [[...before, parent]]);

  // Hidden children disappear from the listing.
  await call('Library.SetLibraryRoots', [
    [...before, { ...parent, excludedChildren: [subPath.toLowerCase()] }],
  ]);
  const hidden = await call('Library.ListShoots', [root]);
  check('listShootsHonoursExcluded', !hidden.some((s) => eq(s.path, subPath)));
  await call('Library.SetLibraryRoots', [[...before, parent]]);

  // ---- watcher -------------------------------------------------------------

  // The scenario the whole feature exists for: create a folder, then copy a
  // card into it. Only a subscription push proves the daemon told us, rather
  // than us re-asking.
  //
  // The two steps are separated deliberately. At mkdir the folder has no
  // photos, so it is correctly absent from the listing — and that dispatch is
  // also when the watcher attaches a watch to it. The copy afterwards fires
  // inside the new folder, never on the parent, so only that child watch can
  // see it. Copying immediately after mkdir would let the first push (which
  // re-reads the disk) find the file and hide a missing child watch.
  const shootSub = subscribe('Library.ListShoots', [root]);
  await until(() => shootSub.pushes.length >= 1);
  const beforeMkdir = shootSub.pushes.length;

  const newShoot = join(root, 'NewShoot');
  mkdirSync(newShoot);
  const mkdirPush = await until(() =>
    shootSub.pushes.length > beforeMkdir ? shootSub.pushes.at(-1) : null,
  );
  check(
    'watcherSeesMkdir',
    !!mkdirPush && !mkdirPush.some((s) => eq(s.path, newShoot)),
    'empty folder announced but not listed',
  );

  const beforeCopy = shootSub.pushes.length;
  copyFileSync(join(FOLDER, raws[0]), join(newShoot, 'IMG_E.ARW'));
  const appeared = await until(() =>
    shootSub.pushes.slice(beforeCopy).some((list) => list.some((s) => eq(s.path, newShoot))),
  );
  check('watcherFindsNewFolder', !!appeared, `${shootSub.pushes.length} pushes`);

  // New photos in an OPEN folder: rows must appear without anyone calling
  // OpenFolder again (ListPhotos only reads the catalog).
  const open = await call('Library.OpenFolder', [subPath]);
  const beforeCount = (await call('Library.ListPhotos', [open.folderId])).length;
  copyFileSync(join(FOLDER, raws[0]), join(subPath, 'IMG_F.ARW'));
  const grew = await until(async () => {
    const list = await call('Library.ListPhotos', [open.folderId]);
    return list.length > beforeCount ? list : null;
  });
  check('watcherSyncsNewPhoto', !!grew, `${beforeCount} → ${grew?.length ?? '?'}`);

  // ...and the passes run over it: metadata, calibration, pre-render. Each has
  // to be polled separately — they run in sequence, so metaLoaded flipping says
  // nothing about the two that follow.
  const photoIs = (pred, ms) =>
    until(async () => {
      const list = await call('Library.ListPhotos', [open.folderId]);
      const p = list.find((x) => x.fileName === 'IMG_F.ARW');
      return p && pred(p) ? p : null;
    }, ms);

  const ingested = await photoIs((p) => p.metaLoaded, 60_000);
  check('watcherRunsMetaPass', !!ingested, ingested ? `w=${ingested.width}` : 'no metaLoaded');

  // A calibrated photo whose measured lift rounds to 0.00 is indistinguishable
  // on the wire from an uncalibrated one (the API flattens SQL NULL to 0). The
  // fixture copies the folder's first ARW, so this is only a reliable signal
  // when that frame has a non-zero lift. folderPasses is sequential, so
  // watcherRunsPrerenderPass passing below independently proves this pass ran.
  const calibrated = await photoIs((p) => p.baseExpEV !== 0, 60_000);
  check(
    'watcherRunsCalibratePass',
    !!calibrated,
    calibrated ? `baseExpEV=${calibrated.baseExpEV}` : 'zero lift — see prerender check',
  );

  if (ingested) {
    // cacheOnly: 200 proves prerenderPass wrote the 2048 rendition. Without it
    // the request would render on demand and prove nothing.
    const res = await until(async () => {
      const r = await fetch(`http://127.0.0.1:8483/img/${ingested.id}/2048?cacheOnly=1`);
      return r.status === 200 ? r : null;
    }, 60_000);
    check('watcherRunsPrerenderPass', !!res, res ? '200' : 'no cached 2048');
  }

  // ---- offline storage -----------------------------------------------------

  // A root whose drive is disconnected: stat fails, so it reports offline and
  // its child listing is empty rather than an error — the rail shows an Offline
  // badge, not a broken block.
  const detached = join(root, 'Detached');
  await call('Library.SetLibraryRoots', [
    [...before, parent, { path: detached, alias: '', includeSubfolders: false, photoCount: 0, isParent: true }],
  ]);
  const offlineStatus = await until(async () => {
    const st = await call('Library.GetRootStatus', []);
    const mine2 = st.find((s) => eq(s.path, detached));
    return mine2 && mine2.online === false ? mine2 : null;
  });
  check('rootReportsOffline', !!offlineStatus);
  const offlineShoots = await call('Library.ListShoots', [detached]);
  check('offlineListShootsIsEmptyNotError', Array.isArray(offlineShoots) && offlineShoots.length === 0);

  // Reconnect it. The poller — not any user action — must notice and flip it.
  mkdirSync(detached);
  copyFileSync(join(FOLDER, raws[0]), join(detached, 'IMG_H.ARW'));
  const backOnline = await until(async () => {
    const st = await call('Library.GetRootStatus', []);
    return st.find((s) => eq(s.path, detached))?.online === true;
  });
  check('rootComesBackOnline', !!backOnline);
  const reattached = await call('Library.ListShoots', [detached]);
  check(
    'onlineRootListsSelfShoot',
    reattached.some((s) => s.isSelf && s.photoCount === 1),
    JSON.stringify(reattached.map((s) => `${s.name}:${s.photoCount}`)),
  );
  await call('Library.SetLibraryRoots', [[...before, parent]]);

  // Re-opening an already-scanned folder must still pick up new files. This is
  // what folderPasses was extracted out of; it worked before this feature and
  // has to keep working.
  copyFileSync(join(FOLDER, raws[0]), join(subPath, 'IMG_G.ARW'));
  const reopened = await call('Library.OpenFolder', [subPath]);
  const after2 = await call('Library.ListPhotos', [reopened.folderId]);
  check(
    'reopenPicksUpNewPhotos',
    after2.some((p) => p.fileName === 'IMG_G.ARW'),
    `photoCount=${reopened.photoCount}`,
  );

  // Restore the original roots list.
  await call('Library.SetLibraryRoots', [before]);
  const restored = await call('Library.GetLibraryRoots', []);
  check('rootsRestored', restored.length === before.length);
} finally {
  ws.close();
  // Background decode jobs may still hold the copied ARWs open (LibRaw
  // fopen) — retry for a while, then leave the temp dir to the OS.
  for (let i = 0; i < 20; i++) {
    try {
      rmSync(root, { recursive: true, force: true });
      break;
    } catch {
      await new Promise((r) => setTimeout(r, 500));
    }
  }
}

const failed = Object.values(results).filter((v) => !v).length;
console.log(failed ? `${failed} ROOTS CHECKS FAILED` : 'ALL ROOTS CHECKS PASSED');
process.exit(failed ? 1 : 0);
