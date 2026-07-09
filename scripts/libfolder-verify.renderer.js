// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Acceptance test for "Add as library folder": the rail must show the child
// folders a managed parent discovered on disk, and a folder created while the
// app is running must appear on its own, with no user action.
//
// Driven by scripts/libfolder-verify.mjs, which seeds the parent root before
// launch and creates the new folder on a timer after it.
const R = {};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms, what) => {
  const t = Date.now();
  for (;;) {
    let v;
    try {
      v = fn();
    } catch {
      v = null;
    }
    if (v) return v;
    if (Date.now() - t > ms) throw new Error(`timeout: ${what}`);
    await sleep(200);
  }
};

const shootRows = () => [...document.querySelectorAll('[data-testid="rail-shoot"]')];
const shootNames = () => shootRows().map((el) => el.dataset.name);
const shootByName = (n) => shootRows().find((el) => el.dataset.name === n);
// The stand-in for an unplugged external drive: a manual root whose folder does
// not exist yet. The driver creates it while the app is running.
const offlineRow = () =>
  [...document.querySelectorAll('[data-testid="rail-root"]')].find((el) =>
    el.dataset.name.endsWith('-offline'),
  );

try {
  const parent = await until(
    () => document.querySelector('[data-testid="rail-parent"]'),
    30000,
    'library-folder header',
  );
  R.parentHeader = parent.textContent.includes('marraw-libfolder');

  // The two seeded subfolders, plus the parent's own loose-RAW row.
  await until(() => shootNames().includes('Ceremony'), 30000, 'discovered children');
  const names = shootNames();
  R.discoveredChildren = names.includes('Ceremony') && names.includes('Reception');
  R.selfShootRow = shootRows().some((el) => el.dataset.self === '1');
  R.noNoiseFolder = !names.includes('export');

  // Counts come from the daemon: Ceremony has 2 RAWs, Reception 1, and the
  // parent's own row counts only the RAW loose in it.
  R.childCounts =
    shootByName('Ceremony').dataset.count === '2' &&
    shootByName('Reception').dataset.count === '1';
  R.selfCountIsFlat = shootRows().find((el) => el.dataset.self === '1').dataset.count === '1';

  // A discovered child cannot be "removed" — it would reappear on the next
  // listing — so it offers Hide instead.
  shootByName('Ceremony').dispatchEvent(
    new MouseEvent('contextmenu', { bubbles: true, clientX: 40, clientY: 120 }),
  );
  const menu = await until(
    () => {
      const el = document.querySelector('[role="menu"]');
      return el && el.innerText ? el.innerText : null;
    },
    5000,
    'child context menu',
  );
  R.childOffersHide = menu.includes('Hide from library');
  R.childHidesRemove = !menu.includes('Remove from library');
  R.childHidesSubfolderToggle = !menu.includes('Include subfolders');
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  await sleep(400);

  // THE acceptance test. The driver creates <parent>/Party and copies a RAW
  // into it a few seconds after launch. Nothing in the UI is touched: the row
  // can only appear because the daemon watched the parent, then watched the new
  // directory, and pushed a fresh listing.
  // A root on storage that is not connected reads as Offline rather than as an
  // empty or broken folder. Asserted before the long waits below — the driver
  // reconnects that folder partway through the run.
  // Match the badge element, not the row's text: the fixture folder is itself
  // named "…-offline", and its own name would satisfy a text match.
  const badged = (el) => !!el.querySelector('[data-testid="offline-badge"]');
  const off = await until(offlineRow, 20000, 'offline root row');
  R.offlineRootBadged =
    off.dataset.online === '0' && badged(off)
      ? true
      : `online=${off.dataset.online} badge=${badged(off)}`;

  R.partyAbsentInitially = !shootNames().includes('Party');
  await until(() => shootNames().includes('Party'), 60000, 'watcher surfaces new folder');
  R.newFolderAppeared = true;

  // The driver creates that missing folder mid-run. No watch can exist on a path
  // that does not exist, so only the availability poller can bring it back.
  await until(() => offlineRow()?.dataset.online === '1', 60000, 'root comes back online');
  R.offlineRootRecovers = true;
  R.offlineBadgeCleared = !badged(offlineRow());

  R.finalRows = shootNames().join(',');
  return R;
} catch (err) {
  R.fatal = String(err && err.stack ? err.stack : err);
  return R;
}
