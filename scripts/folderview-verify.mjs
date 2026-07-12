// End-to-end check of per-folder library view settings (folderViews) against
// a running `marrawd --dev --port 8483`. Exercises: SetFolderView writes and
// patch-merge semantics (nil fields untouched), the folderViews map in the
// GetUISettings snapshot, validation rejects, and that the global
// librarySort/gapMinutes rows are untouched by per-folder writes.
//
//   node scripts/folderview-verify.mjs <folder-A> <folder-B> [--check-only]
//
// --check-only skips the writes and only asserts folder A's stored view —
// run it after a daemon restart to prove the settings persist on disk.

const [FOLDER_A, FOLDER_B] = process.argv.slice(2, 4);
const CHECK_ONLY = process.argv.includes('--check-only');
if (!FOLDER_A || !FOLDER_B) {
  console.error('usage: node scripts/folderview-verify.mjs <folder-A> <folder-B> [--check-only]');
  process.exit(1);
}
const keyA = FOLDER_A.toLowerCase();
const keyB = FOLDER_B.toLowerCase();

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
    }, 30_000);
  });
}

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const rejects = async (name, promise) => {
  try {
    await promise;
    check(false, `${name} (no error raised)`);
  } catch {
    check(true, name);
  }
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

if (!CHECK_ONLY) {
  const a = await call('Library.OpenFolder', [FOLDER_A]);
  const b = await call('Library.OpenFolder', [FOLDER_B]);
  console.log(`opened A (folderId ${a.folderId}) and B (folderId ${b.folderId})`);

  const before = await call('Settings.GetUISettings', []);

  // The full A view: rating 3+, picks only, name-descending, gap off (0).
  await call('Settings.SetFolderView', [
    keyA,
    { minRating: 3, flagFilter: 'pick', librarySort: 'nameDesc', gapMinutes: 0 },
  ]);
  let s = await call('Settings.GetUISettings', []);
  let v = s.folderViews?.[keyA];
  check(!!v, 'folder A has a folderViews entry');
  check(v?.minRating === 3, `A.minRating stored (${v?.minRating})`);
  check(v?.flagFilter === 'pick', `A.flagFilter stored (${v?.flagFilter})`);
  check(v?.librarySort === 'nameDesc', `A.librarySort stored (${v?.librarySort})`);
  check(v?.gapMinutes === 0, `A.gapMinutes stored as 0/off (${v?.gapMinutes})`);
  check(s.folderViews?.[keyB] === undefined, 'folder B has no entry');

  // Patch merge: change only the gap — the other three fields must survive.
  await call('Settings.SetFolderView', [keyA, { gapMinutes: 30 }]);
  s = await call('Settings.GetUISettings', []);
  v = s.folderViews?.[keyA];
  check(v?.gapMinutes === 30, `patch updates gap (${v?.gapMinutes})`);
  check(
    v?.minRating === 3 && v?.flagFilter === 'pick' && v?.librarySort === 'nameDesc',
    'patch leaves the other fields untouched',
  );

  // Per-folder writes must not move the global last-used rows.
  check(s.librarySort === before.librarySort, `global librarySort untouched (${s.librarySort})`);
  check(s.gapMinutes === before.gapMinutes, `global gapMinutes untouched (${s.gapMinutes})`);

  // Validation rejects.
  await rejects('rejects empty path', call('Settings.SetFolderView', ['  ', { minRating: 1 }]));
  await rejects('rejects minRating 9', call('Settings.SetFolderView', [keyA, { minRating: 9 }]));
  await rejects(
    'rejects unknown flagFilter',
    call('Settings.SetFolderView', [keyA, { flagFilter: 'starred' }]),
  );
  await rejects(
    'rejects unknown librarySort',
    call('Settings.SetFolderView', [keyA, { librarySort: 'shuffle' }]),
  );
  await rejects(
    'rejects gapMinutes 100000',
    call('Settings.SetFolderView', [keyA, { gapMinutes: 100000 }]),
  );

  // A second folder gets its own independent entry.
  await call('Settings.SetFolderView', [keyB, { librarySort: 'captureDesc' }]);
  s = await call('Settings.GetUISettings', []);
  check(s.folderViews?.[keyB]?.librarySort === 'captureDesc', 'folder B entry independent');
  check(s.folderViews?.[keyB]?.minRating === undefined, 'B has no minRating (sparse patch)');
  check(s.folderViews?.[keyA]?.minRating === 3, 'A entry unaffected by B write');
} else {
  // Post-restart: everything written above must come back from disk.
  const s = await call('Settings.GetUISettings', []);
  const v = s.folderViews?.[keyA];
  check(
    v?.minRating === 3 && v?.flagFilter === 'pick' && v?.librarySort === 'nameDesc' && v?.gapMinutes === 30,
    `folder A view survives a daemon restart (${JSON.stringify(v)})`,
  );
  check(s.folderViews?.[keyB]?.librarySort === 'captureDesc', 'folder B view survives too');
}

console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECKS FAILED`);
ws.close();
process.exit(failures === 0 ? 0 : 1);
