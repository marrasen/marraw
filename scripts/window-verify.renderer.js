// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Reports where the shell actually put this window, whether a pop-out viewer
// came back with it, and how the info aside was restored. Asserts nothing and
// changes nothing — scripts/window-verify.mjs seeds preferences.json before
// each launch and decides what the answers should be.
const R = {};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

try {
  // A first approximation only: since Electron 43 a frameless window on Linux
  // sits inside client-side decoration insets, so these describe the widget
  // rather than the window. The shell overrides x/y/width/height with its own
  // getBounds() (UITEST_WINDOW) before the harness compares anything.
  R.x = window.screenX;
  R.y = window.screenY;
  R.width = window.outerWidth;
  R.height = window.outerHeight;
  // The work area of whichever display the window landed on, so the harness
  // can tell "somewhere sensible" from "off the edge of every screen" without
  // knowing this machine's monitors.
  R.availLeft = window.screen.availLeft ?? 0;
  R.availTop = window.screen.availTop ?? 0;
  R.availWidth = window.screen.availWidth;
  R.availHeight = window.screen.availHeight;

  // Settled state, after the first uiSettings snapshot has landed — before it,
  // the store is showing its own defaults rather than anything persisted.
  const ui = window.__marraw.useUIStore;
  for (let i = 0; i < 100 && !ui.getState().settingsLoaded; i++) await sleep(100);
  R.settingsLoaded = ui.getState().settingsLoaded === true;
  R.showEditPanel = ui.getState().showEditPanel;

  R.viewerOpen = (await window.win.getViewerPhoto()).open === true;
} catch (err) {
  R.fatal = String(err);
}
return R;
