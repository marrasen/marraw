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
  R.partyAbsentInitially = !shootNames().includes('Party');
  await until(() => shootNames().includes('Party'), 60000, 'watcher surfaces new folder');
  R.newFolderAppeared = true;
  R.finalRows = shootNames().join(',');
  return R;
} catch (err) {
  R.fatal = String(err && err.stack ? err.stack : err);
  return R;
}
