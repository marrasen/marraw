// Checks the "reopen the folder you had open" setting against a running
// `marrawd --dev --port 8483`: it round-trips through the settings table,
// survives a reconnect, clears, and refuses a path no filesystem could hold.
//
//   node scripts/lastfolder-verify.mjs <raw-folder>
//
// The reopening itself is a client behaviour — see the `restore` surface in
// scripts/shot.renderer.js for that half.

import { connect } from './lib/rpc.mjs';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/lastfolder-verify.mjs <raw-folder>');
  process.exit(1);
}

const { call, close } = await connect();

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};

const settings = () => call('Settings.GetUISettings', []);
const before = await settings();
console.log(`lastFolder was ${JSON.stringify(before.lastFolder)}`);

await call('Settings.SetLastFolder', [FOLDER]);
check((await settings()).lastFolder === FOLDER, 'a folder path round-trips');

// A second connection is what the next launch actually is.
const second = await connect();
check((await second.call('Settings.GetUISettings', [])).lastFolder === FOLDER, 'survives a reconnect');
second.close();

await call('Settings.SetLastFolder', ['']);
check((await settings()).lastFolder === '', 'empty clears it');

await call('Settings.SetLastFolder', [FOLDER]);
let rejected = false;
try {
  await call('Settings.SetLastFolder', ['/' + 'x'.repeat(5000)]);
} catch {
  rejected = true;
}
check(rejected, 'a 5 KB path is rejected');
check((await settings()).lastFolder === FOLDER, 'the rejected write left the value alone');

// A path that does not exist is stored as-is: it is the client that has to
// notice, so it can say so and show the library.
await call('Settings.SetLastFolder', ['/no/such/folder']);
check((await settings()).lastFolder === '/no/such/folder', 'a missing path is stored, not second-guessed');
let openFailed = false;
try {
  await call('Library.OpenFolder', ['/no/such/folder']);
} catch {
  openFailed = true;
}
check(openFailed, 'opening a missing path errors (what the client reports)');

await call('Settings.SetLastFolder', [before.lastFolder]);
console.log(failures ? `${failures} FAILED` : 'all passed');
close();
process.exit(failures ? 1 : 0);
