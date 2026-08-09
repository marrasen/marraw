// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Flips the info aside with its own toolbar button and proves the new value
// went all the way to the daemon and back: writing the setting triggers a
// fresh uiSettings snapshot to every window, and applyUISettings overwrites
// the store from it. So a value that is still flipped once that snapshot has
// landed is the daemon's answer, not the optimistic local one.
//
// Driven by scripts/window-verify.mjs, which reads the persisted result back
// on the NEXT launch — the half no single run can see.
const R = {};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms, what) => {
  const t = Date.now();
  for (;;) {
    const v = fn();
    if (v) return v;
    if (Date.now() - t > ms) throw new Error(`timeout: ${what}`);
    await sleep(200);
  }
};

try {
  R.x = window.screenX;
  R.y = window.screenY;
  R.width = window.outerWidth;
  R.height = window.outerHeight;
  R.availLeft = window.screen.availLeft ?? 0;
  R.availTop = window.screen.availTop ?? 0;
  R.availWidth = window.screen.availWidth;
  R.availHeight = window.screen.availHeight;

  R.viewerOpen = (await window.win.getViewerPhoto()).open === true;

  const ui = window.__marraw.useUIStore;
  await until(() => ui.getState().settingsLoaded, 30000, 'settings loaded');
  await until(() => ui.getState().visibleIds.length > 0, 30000, 'photos loaded');
  R.before = ui.getState().showEditPanel;

  // The toolbar toggle itself, not the store action — the write-through is
  // what this is testing.
  const label = R.before ? 'Hide develop panel' : 'Show develop panel';
  const btn = await until(
    () => [...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === label),
    15000,
    `${label} button`,
  );
  btn.click();

  // Long enough for the RPC, the settings row write and the refresh push. A
  // failed write would have reverted the optimistic value by now.
  await sleep(2500);
  R.after = ui.getState().showEditPanel;
  R.flipped = R.after === !R.before;
} catch (err) {
  R.fatal = String(err);
}
return R;
