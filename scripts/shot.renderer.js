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
  // Hover the image so the magnifier + RGB readout render.
  await sleep(1500);
  const box = document.querySelector('.overflow-auto .m-auto');
  if (box) {
    const r = box.getBoundingClientRect();
    box.dispatchEvent(
      new PointerEvent('pointermove', {
        bubbles: true,
        clientX: r.left + r.width * 0.45,
        clientY: r.top + r.height * 0.55,
      }),
    );
  }
} else if (shot === 'addfolder') {
  ui().setAddFolderOpen(true);
} else if (shot === 'shortcuts') {
  ui().setShortcutsOpen(true);
} else if (shot === 'light') {
  document.documentElement.classList.remove('dark');
} else if (shot === 'palette') {
  ui().setPaletteOpen(true);
} else if (shot === 'export') {
  ui().setExportOpen(true);
} else if (shot === 'export-raw') {
  ui().setExportOpen(true);
  await sleep(400);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'RAW + XMP')?.click();
} else if (shot === 'export-inplace') {
  ui().setExportOpen(true);
  await sleep(400);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'RAW + XMP')?.click();
  await sleep(200);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'Use current folder')?.click();
} else if (shot === 'settings') {
  ui().setSettingsOpen(true);
} else if (shot === 'sidecars') {
  ui().setSettingsOpen(true);
  await sleep(300);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'Sidecars')?.click();
} else if (shot === 'cache') {
  ui().setSettingsOpen(true);
  await sleep(300);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'Cache')?.click();
} else if (shot === 'develop-light') {
  document.documentElement.classList.remove('dark');
  // setState on the mirror: shows the dials without persisting server-side.
  mw.useUIStore.setState({ quickDials: ['expEV', 'contrast', 'toneHighlights', 'toneShadows', 'wbTemp', 'wbMode'] });
  ui().setMode('develop');
} else if (shot === 'cull-light') {
  document.documentElement.classList.remove('dark');
  mw.useUIStore.setState({ cullDials: ['expEV', 'contrast', 'wbTemp'] });
  ui().setMode('cull');
} else if (shot === 'toolbars') {
  ui().setSettingsOpen(true);
  await sleep(300);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'Toolbars')?.click();
} else if (shot === 'cull-dials') {
  mw.useUIStore.setState({ cullDials: ['expEV', 'contrast', 'wbTemp'] });
  ui().setMode('cull');
} else if (shot === 'develop-dials') {
  mw.useUIStore.setState({ quickDials: ['expEV', 'contrast', 'toneHighlights', 'toneShadows', 'wbTemp', 'vibrance'] });
  ui().setMode('develop');
} else if (shot === 'batch') {
  const ids = ui().visibleIds;
  ui().focus(ids[2]);
  for (const id of ids.slice(3, 14)) ui().focus(id, { toggle: true });
} else if (shot === 'watermark' || shot === 'watermark-portrait') {
  // Drive the editor like a user — create, rename, type — so every step
  // exercises the live-write path. React inputs need the native setter.
  const setInput = (el, v) => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(el, v);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const btn = (label) =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === label);
  ui().setWatermarkEditorOpen(true);
  await sleep(400);
  btn('New watermark')?.click();
  await sleep(300);
  const nameInput = document.querySelector('input[aria-label="Watermark name"]');
  if (nameInput) setInput(nameInput, 'UITEST watermark');
  const textInput = document.querySelector('input[aria-label="Watermark text"]');
  if (textInput) setInput(textInput, '© Marcus Johansson');
  if (shot === 'watermark-portrait') {
    await sleep(300);
    btn('Portrait')?.click();
  }
  // Fonts + canvas settle, then probe the preview: white-ish text pixels
  // must exist in the bottom-right quadrant (default anchor) and nowhere in
  // the top-left one.
  await sleep(2500);
  // The app renders other canvases (histogram) — scope to the dialog.
  const canvas = document.querySelector('[role="dialog"] canvas');
  let textDrawn = false;
  if (canvas) {
    const ctx = canvas.getContext('2d');
    const lit = (x0, y0, w, h) => {
      const d = ctx.getImageData(x0, y0, w, h).data;
      let n = 0;
      for (let i = 0; i < d.length; i += 4) {
        if (d[i] > 225 && d[i + 1] > 225 && d[i + 2] > 225) n++;
      }
      return n;
    };
    const br = lit(canvas.width / 2, canvas.height / 2, canvas.width / 2, canvas.height / 2);
    const tl = lit(0, 0, canvas.width / 2, canvas.height / 2);
    textDrawn = br > 50 && tl === 0;
  }
  window.__wmProbe = { textDrawn, canvas: !!canvas };
}
// Let previews decode, then wake the chrome (capture fires on resolve).
await sleep(3600);
window.dispatchEvent(new PointerEvent('pointermove', { clientX: 500, clientY: 300 }));
await sleep(400);
return window.__wmProbe ? { shot, ...window.__wmProbe } : shot;
