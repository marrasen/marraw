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
} else if (shot === 'crop' || shot === 'crop-exit') {
  ui().setMode('develop');
  await until(() => mw.useEditSession.getState().draft != null);
  const zoomBefore = ui().loupeZoom;
  mw.esSetCropping(true);
  // Wait for the flat frame so loupeFitScale mirrors the crop-mode geometry.
  await until(() => mw.useEditSession.getState().preview?.flat);
  await sleep(600);
  const zoomInCrop = ui().loupeZoom;
  const fitInCrop = ui().loupeFitScale;
  let zoomAfterExit = null;
  if (shot === 'crop-exit') {
    mw.esSetCropping(false);
    await sleep(600);
    zoomAfterExit = ui().loupeZoom;
  }
  window.__cropProbe = { zoomBefore, zoomInCrop, fitInCrop, zoomAfterExit };
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
} else if (shot === 'masks') {
  // Local adjustments: add a radial mask with a strong warm lift, keep it
  // selected so the overlay handles show, and probe that the preview pixels
  // actually changed inside the mask but not outside.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200); // initial preview settles
  const pixelsAt = async (blob) => {
    const bmp = await createImageBitmap(blob, { resizeWidth: 64 });
    const c = document.createElement('canvas');
    c.width = bmp.width;
    c.height = bmp.height;
    const ctx = c.getContext('2d');
    ctx.drawImage(bmp, 0, 0);
    const d = ctx.getImageData(0, 0, bmp.width, bmp.height).data;
    const px = (fx, fy) => {
      const i = (Math.floor(fy * (bmp.height - 1)) * bmp.width + Math.floor(fx * (bmp.width - 1))) * 4;
      return [d[i], d[i + 1], d[i + 2]];
    };
    return { center: px(0.5, 0.5), corner: px(0.03, 0.03) };
  };
  // Idempotence: a previous run's masks persisted to the fixture photo —
  // start from a mask-free state. The commit also lands the sharp 2048
  // settle of the base params, so the before/after comparison is
  // settle-to-settle (a 1024 draft frame differs from the 2048 by resample
  // noise). No preview blob exists until an edit renders one.
  mw.esUpdate({ masks: [] });
  mw.esCommit();
  await until(() => es.getState().preview?.blob && mw.esPreviewSettled(), 30000);
  const before = await pixelsAt(es.getState().preview.blob);
  mw.esAddMask('radial');
  await sleep(300);
  const idx = (es.getState().draft.masks?.length ?? 1) - 1;
  mw.esUpdateMask(idx, { adjust: { expEV: 1.5, temp: 0.6 } });
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);
  const after = await pixelsAt(es.getState().preview.blob);
  const luma = (p) => (p[0] * 299 + p[1] * 587 + p[2] * 114) / 1000;

  // Keyboard tour: with a second mask added (selected, no slider focused),
  // ↓ enters its first slider, ↑↑ crosses back onto the previous mask's last
  // slider, +/- steps the focused slider, Tab cycles develop→masks.
  const press = (key, opts = {}) =>
    window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, ...opts }));
  mw.esAddMask('linear'); // becomes index 1, selected
  await sleep(200);
  press('ArrowDown');
  const focusEnter = { mask: es.getState().activeMask, ctrl: es.getState().activeMaskControl };
  press('ArrowUp');
  press('ArrowUp');
  const focusCrossed = { mask: es.getState().activeMask, ctrl: es.getState().activeMaskControl };
  const stepBase =
    es.getState().draft.masks[focusCrossed.mask]?.adjust?.[focusCrossed.ctrl] ?? 0;
  press('+');
  press('+');
  await sleep(100);
  const stepped =
    es.getState().draft.masks[focusCrossed.mask]?.adjust?.[focusCrossed.ctrl] ?? 0;
  ui().setDevelopTab('develop');
  press('Tab');
  const tabAfterDevelop = ui().developTab;
  // Restore the single-mask state for the screenshot + repeat runs.
  press('Escape'); // clears mask selection/focus
  mw.esUpdateMask(1, { adjust: {} });
  await sleep(100);

  window.__maskProbe = {
    maskCount: es.getState().draft.masks?.length ?? 0,
    centerLumaBefore: Math.round(luma(before.center)),
    centerLumaAfter: Math.round(luma(after.center)),
    centerBrightened: luma(after.center) > luma(before.center) + 8,
    cornerLumaBefore: Math.round(luma(before.corner)),
    cornerLumaAfter: Math.round(luma(after.corner)),
    cornerUnchanged: Math.abs(luma(after.corner) - luma(before.corner)) <= 3,
    // ↓ on the freshly added mask 1 lands on its first slider…
    focusEnter,
    // …and ↑↑ walks back across the boundary into mask 0 (last slider, then
    // one more up).
    focusCrossed,
    stepDelta: Math.round((stepped - stepBase) * 1000) / 1000,
    tabAfterDevelop,
    escCleared: es.getState().activeMask == null && es.getState().activeMaskControl == null,
  };
} else if (shot === 'aitint') {
  // AI mask hover tint: generate a subject mask via the real button, hover
  // its row header, and assert the server-rendered red tint appears over the
  // loupe (the only visualization an AI mask has).
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('masks');
  await sleep(600);
  mw.esUpdate({ masks: [] }); // idempotence: drop persisted masks first
  mw.esCommit();
  await sleep(800);
  document.querySelector('[data-testid="ai-mask-subject"]')?.click();
  // Generation runs a local model (seconds warm; the map may also already
  // exist from a previous run) and adds the mask on success.
  await until(() => (es.getState().draft?.masks ?? []).some((m) => m.type === 'ai'), 120000);
  await sleep(500);
  // Hover the mask row header (React onMouseEnter listens to mouseover).
  const row = [...document.querySelectorAll('span')].find((s) => s.textContent.startsWith('Subject '));
  row?.parentElement?.parentElement?.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
  const tintImg = () => document.querySelector('[data-testid="mask-hover-tint"] img');
  await until(() => tintImg()?.complete && tintImg()?.naturalWidth > 0, 15000);
  await sleep(400); // fade-in settles for the screenshot
  window.__maskProbe = {
    aiMask: true,
    tintShown: !!tintImg(),
    tintW: tintImg()?.naturalWidth ?? 0,
    tintH: tintImg()?.naturalHeight ?? 0,
  };
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
} else if (shot === 'render-progress') {
  // 1:1 on a photo whose tile grid is cold → the decoding indicator must
  // upgrade from indeterminate to a live percent (RenderProgressEvent), and
  // the render must eventually land. Focus the LAST photo: verify scripts
  // tend to warm the first one.
  ui().setMode('develop');
  ui().focus(ui().visibleIds[ui().visibleIds.length - 1]);
  await sleep(800);
  ui().setLoupeZoom(1);
  const badgeText = () =>
    [...document.querySelectorAll('span')].map((s) => s.textContent).find((t) => /1:1 tile/.test(t ?? ''));
  let sawPercent = null;
  const t0 = Date.now();
  while (Date.now() - t0 < 20000) {
    const t = badgeText();
    const m = t && t.match(/1:1 tile · (\d+)%/);
    if (m) sawPercent = Number(m[1]);
    // Rendered: the tile badge left and we saw progress — done probing.
    if (sawPercent != null && !t) break;
    await sleep(80);
  }
  window.__renderProbe = { sawPercent, badgeGone: !badgeText() };
} else if (shot === 'settle') {
  // Probes the immediate-settle scheduler: (1) a one-shot apply must land its
  // low-res frame and then the sharp 2048 with NO dead gap between them (the
  // old 200ms settle timer would show as rendering===0 polls in between);
  // (2) an edit while the 2048 is in flight must abort it and land a fast
  // low-res frame instead of waiting the full render out.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1500); // initial preview + decode warm-up
  const events = [];
  const polls = [];
  es.subscribe((s, prev) => {
    if (s.preview !== prev.preview && s.preview) {
      events.push({ t: performance.now(), size: s.preview.blob.size });
    }
  });
  let polling = true;
  const pollLoop = (async () => {
    while (polling) {
      polls.push({ t: performance.now(), rendering: es.getState().rendering });
      await new Promise((r) => setTimeout(r, 10));
    }
  })();

  // Probe 1: auto-apply → low-res then full, back to back.
  await mw.esAuto(['all']);
  await until(() => mw.esPreviewSettled(), 30000);
  await sleep(60);
  const p1 = events.slice();
  let deadGapPolls = -1;
  if (p1.length >= 2) {
    const t1 = p1[0].t + 5;
    const t2 = p1[p1.length - 1].t - 5;
    deadGapPolls = polls.filter((p) => p.t > t1 && p.t < t2 && p.rendering === 0).length;
  }

  // Probe 2: supersede an in-flight 2048. Commit starts the settle; an edit
  // 120ms in must abort it and land a small (draft-size) frame promptly.
  events.length = 0;
  const exp = es.getState().draft.expEV ?? 0;
  mw.esUpdate({ expEV: Math.round((exp + 0.4) * 100) / 100 });
  await until(() => events.length >= 1, 15000); // drag frame landed
  mw.esCommit(); // 2048 settle starts
  await sleep(120);
  const renderingMidSettle = es.getState().rendering;
  const tSupersede = performance.now();
  mw.esUpdate({ expEV: Math.round((exp + 0.7) * 100) / 100 });
  await until(() => events.some((e) => e.t > tSupersede), 15000);
  const afterSupersede = events.find((e) => e.t > tSupersede);
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);
  await sleep(60);
  polling = false;
  await pollLoop;
  const sizes = events.map((e) => Math.round(e.size / 1024));
  const maxSize = Math.max(...events.map((e) => e.size));
  window.__settleProbe = {
    // Probe 1: >=2 frames, small→large, zero dead-gap polls between them.
    p1Frames: p1.map((e) => Math.round(e.size / 1024)),
    p1SettledSharp: p1.length >= 2 && p1[p1.length - 1].size > p1[0].size,
    deadGapPolls,
    // Probe 2: a render was in flight at supersede time, the next landed
    // frame is draft-sized (not the aborted 2048), latency from supersede.
    renderingMidSettle,
    supersededFrameKB: afterSupersede ? Math.round(afterSupersede.size / 1024) : null,
    supersededIsDraft: !!afterSupersede && afterSupersede.size < maxSize * 0.55,
    supersedeLatencyMs: afterSupersede ? Math.round(afterSupersede.t - tSupersede) : null,
    p2FramesKB: sizes,
    settled: mw.esPreviewSettled(),
  };
} else if (shot === 'welcome') {
  // The landing page (library has shoots, none open). The harness's opened
  // folder guarantees a root exists; stepping out of it lands on Welcome.
  // Seed lastSeenVersion HERE, after the auto-open folder settled — a
  // transient Welcome mount during startup (openFolder still in flight)
  // consumes any earlier seed and marks the current version seen. Pass an
  // old version via ?seedLastSeen= to show the "What's new" card, or skip
  // the param to shoot whatever state the daemon holds.
  const seed = new URLSearchParams(location.search).get('seedLastSeen');
  let afterSeed = null;
  if (seed != null) {
    mw.setLastSeenVersion(seed);
    afterSeed = ui().lastSeenVersion;
    await sleep(300);
  }
  const beforeMount = ui().lastSeenVersion;
  mw.useUIStore.setState({ folderId: null, folderPath: null });
  await sleep(600);
  const card = [...document.querySelectorAll('h3')].find((h) =>
    /What's new/.test(h.textContent ?? ''),
  );
  window.__welcomeProbe = {
    cardShown: !!card,
    bullets: card ? card.parentElement.querySelectorAll('li').length : 0,
    lastSeen: ui().lastSeenVersion,
    afterSeed,
    beforeMount,
    welcomeMounted: [...document.querySelectorAll('h2')].some((h) =>
      /Welcome to marraw/.test(h.textContent ?? ''),
    ),
    entries: mw.changelog ? mw.changelog.parseChangelog().length : 'no bridge',
  };
} else if (shot === 'folderview') {
  // Per-folder view memory: set filters through the real FilterBar in folder
  // A (sort/gap via the bridge — the sort menu is click-trappy), hop to
  // ?altFolder= (folder B) and expect the mixed fallback (filters reset,
  // sort/gap follow last-used), hop back and expect A's view restored whole.
  // The capture shows folder A's FilterBar: 3 lit stars, Picks, gap Off.
  const alt = new URLSearchParams(location.search).get('altFolder');
  const pathA = ui().folderPath;
  const view = () => {
    const { minRating, flagFilter, librarySort, gapMinutes } = ui();
    return { minRating, flagFilter, librarySort, gapMinutes };
  };
  document.querySelector('button[aria-label="Show 3+ stars"]')?.click();
  [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Picks')?.click();
  mw.setLibrarySort('nameDesc');
  mw.setGapMinutes(null);
  await sleep(1000); // server write + echo round-trip
  const inA = view();
  await mw.openPath(alt);
  await until(() => ui().folderPath === alt);
  await sleep(1000);
  const inB = view();
  mw.setLibrarySort('captureAsc'); // give B its own view; A must not care
  await sleep(500);
  await mw.openPath(pathA);
  await until(() => ui().folderPath === pathA);
  await sleep(1000);
  const backInA = view();
  window.__folderViewProbe = { inA, inB, backInA };
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
const probe =
  window.__wmProbe ??
  window.__maskProbe ??
  window.__cropProbe ??
  window.__renderProbe ??
  window.__settleProbe ??
  window.__welcomeProbe ??
  window.__folderViewProbe;
return probe ? { shot, ...probe } : shot;
