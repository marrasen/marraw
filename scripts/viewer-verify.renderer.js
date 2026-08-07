// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Acceptance test for the pop-out photo window (Ctrl+N): the shell must open
// and close it on the toggle, and the window must be told which photo the main
// window has focused — including a photo focused after it was already open.
//
// Driven by scripts/viewer-verify.mjs. Everything is asserted from THIS window:
// the harness only ever attaches to the initial one, and the main process's
// getViewerPhoto reports both halves of the state (is a viewer up, and on which
// photo), so driving the second window is unnecessary.
const R = {};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms, what) => {
  const t = Date.now();
  for (;;) {
    let v;
    try {
      v = await fn(); // every probe here is async — awaiting is what makes it retry
    } catch {
      v = null;
    }
    if (v) return v;
    if (Date.now() - t > ms) throw new Error(`timeout: ${what}`);
    await sleep(200);
  }
};

try {
  const ui = window.__marraw.useUIStore;
  R.bridgePresent = typeof window.win?.toggleViewer === 'function';

  // A folder with at least two photos, so the "follows a later focus" step has
  // somewhere to move to.
  const ids = await until(
    () => {
      const v = ui.getState().visibleIds;
      return v.length >= 2 ? v : null;
    },
    30000,
    'photos loaded',
  );
  const folderId = ui.getState().folderId;
  ui.getState().focus(ids[0]);
  await sleep(600);

  // --- 1. Closed to begin with, but the focused photo is already cached. ---
  let state = await window.win.getViewerPhoto();
  R.closedBeforeToggle = state.open === false;
  R.focusCachedWhileClosed = state.folderId === folderId && state.photoId === ids[0];

  // --- 2. Ctrl+N opens it, on the photo that was focused. ---
  window.win.toggleViewer();
  state = await until(
    async () => {
      const s = await window.win.getViewerPhoto();
      return s.open ? s : null;
    },
    15000,
    'viewer opens',
  );
  R.opensOnToggle = true;
  R.opensOnFocusedPhoto = state.folderId === folderId && state.photoId === ids[0];

  // --- 3. Navigating in this window re-points the open viewer. ---
  ui.getState().focus(ids[1]);
  state = await until(
    async () => {
      const s = await window.win.getViewerPhoto();
      return s.photoId === ids[1] ? s : null;
    },
    10000,
    'viewer follows focus',
  );
  R.followsFocus = state.open === true && state.photoId === ids[1];

  // --- 4. The same key closes it. ---
  window.win.toggleViewer();
  await until(
    async () => {
      const s = await window.win.getViewerPhoto();
      return s.open === false ? s : null;
    },
    15000,
    'viewer closes',
  );
  R.closesOnSecondToggle = true;
} catch (err) {
  R.fatal = String(err);
}
return R;
