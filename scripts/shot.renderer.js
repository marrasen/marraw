// Screenshot driver (runs inside the MARRAW_UITEST async wrapper): puts the
// app into the surface named by the ?shot= query param, waits for previews
// to decode, and wakes the auto-hiding chrome right before the capture.
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms = 30000) => {
  const t = Date.now();
  for (;;) {
    let v;
    try { v = fn(); } catch { v = null; }
    if (v) return v;
    if (Date.now() - t > ms) throw new Error('timeout');
    await sleep(100);
  }
};
const mw = await until(() => window.__marraw);
const ui = () => mw.useUIStore.getState();
await until(() => ui().visibleIds.length > 0);
const shot = new URLSearchParams(location.search).get('shot') || 'cull';

ui().focus(ui().visibleIds[6] ?? ui().visibleIds[0]);
await sleep(300);
if (shot === 'cull') {
  ui().setMode('cull');
} else if (shot === 'sheet') {
  ui().setMode('cull');
  await sleep(300);
  ui().setContactSheet(true);
} else if (shot === 'develop') {
  ui().setMode('develop');
} else if (shot === 'crop') {
  ui().setMode('develop');
  await until(() => mw.useEditSession.getState().draft != null);
  mw.esSetCropping(true);
} else if (shot === 'wb') {
  ui().setMode('develop');
  await until(() => mw.useEditSession.getState().draft != null);
  mw.useEditSession.setState({ wbPicking: true });
}
// Let previews decode, then wake the chrome (capture fires on resolve).
await sleep(3600);
window.dispatchEvent(new PointerEvent('pointermove', { clientX: 500, clientY: 300 }));
await sleep(400);
return shot;
