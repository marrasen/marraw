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
} else if (shot === 'autocrop') {
  // Subject auto crop: enter crop mode, pick 3:2, click the real Auto button,
  // and assert the committed rect is a proper sub-frame crop whose pixel
  // ratio matches the preset. Also regression-probes the crop-mode arrow-key
  // fix: ↑ must neither focus a develop control nor leave crop / switch photo.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(800);
  // Idempotence: reset the geometry a previous run committed.
  mw.esUpdate({ rotate: 0, flipH: false, cropX: 0, cropY: 0, cropW: 0, cropH: 0, cropAngle: 0 });
  mw.esCommit();
  await sleep(300);
  mw.esSetCropping(true);
  await until(() => es.getState().preview?.flat);
  await sleep(400);
  const focusBefore = ui().focusId;
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }));
  await sleep(200);
  const arrowStaysPut =
    es.getState().cropping === true &&
    es.getState().activeControl == null &&
    ui().focusId === focusBefore;
  const btn = (label) =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === label);
  btn('3:2')?.click();
  await sleep(600);
  const before = { ...es.getState().draft };
  btn('Auto')?.click();
  await until(() => {
    const d = es.getState().draft;
    return d && (d.cropX !== before.cropX || d.cropW !== before.cropW || d.cropY !== before.cropY || d.cropH !== before.cropH);
  }, 120000);
  await sleep(600);
  const d = es.getState().draft;
  // The overlay's center pill shows "<ratio><W × H>" from the committed
  // rect; its ratio label is the user-facing truth the preset was honored.
  const pillText = document.querySelector('[data-testid="crop-overlay"]')?.textContent ?? '';
  window.__autoCropProbe = {
    arrowStaysPut,
    crop: { x: +d.cropX.toFixed(4), y: +d.cropY.toFixed(4), w: +d.cropW.toFixed(4), h: +d.cropH.toFixed(4) },
    subFrame: d.cropW > 0 && d.cropH > 0 && (d.cropW < 1 || d.cropH < 1),
    insideFrame: d.cropX >= 0 && d.cropY >= 0 && d.cropX + d.cropW <= 1.0001 && d.cropY + d.cropH <= 1.0001,
    pill: pillText,
    ratioIs32: pillText.startsWith('3:2'),
  };
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
} else if (shot === 'aidialog') {
  // Download-consent dialog: with the subject model hidden (the shot wrapper
  // renames it beforehand), clicking Subject must ask instead of fetching.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('masks');
  await sleep(800);
  document.querySelector('[data-testid="ai-mask-subject"]')?.click();
  await until(() => document.querySelector('[data-testid="ai-model-dialog"]'), 15000);
  await sleep(300);
  window.__maskProbe = {
    dialogShown: !!document.querySelector('[data-testid="ai-model-dialog"]'),
    dialogText: document.querySelector('[data-testid="ai-model-dialog"]')?.textContent?.slice(0, 240) ?? '',
  };
} else if (shot === 'aiscene') {
  // Scene detection chips: click the Scene button, wait for the detected
  // category chips, add a mask from the largest one.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('masks');
  await sleep(600);
  mw.esUpdate({ masks: [] });
  mw.esCommit();
  await sleep(800);
  document.querySelector('[data-testid="ai-mask-scene"]')?.click();
  await until(() => document.querySelector('[data-testid="scene-chips"]'), 120000);
  await sleep(300);
  const chips = [...document.querySelectorAll('[data-testid="scene-chips"] button')];
  chips[0]?.click();
  await until(() => (es.getState().draft?.masks ?? []).some((m) => m.aiKind === 'class'), 5000);
  await sleep(400);
  window.__maskProbe = {
    chips: chips.map((c) => c.textContent),
    classMaskAdded: (es.getState().draft?.masks ?? []).some((m) => m.aiKind === 'class'),
  };
} else if (shot === 'depthrange') {
  // Depth window as ONE two-thumb range row: generate a depth mask via the
  // real button, move the window through the store, and assert the "Depth
  // range" slider renders two thumbs whose display mirrors the mask params.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('masks');
  await sleep(600);
  mw.esUpdate({ masks: [] }); // idempotence: drop persisted masks first
  mw.esCommit();
  await sleep(800);
  document.querySelector('[data-testid="ai-mask-depth"]')?.click();
  await until(() => (es.getState().draft?.masks ?? []).some((m) => m.aiKind === 'depth'), 120000);
  await sleep(500);
  const idx = (es.getState().draft?.masks ?? []).findIndex((m) => m.aiKind === 'depth');
  mw.esUpdateMask(idx, { depthLo: 0.35, depthHi: 0.8 });
  mw.esCommit();
  await sleep(500);
  const row = [...document.querySelectorAll('span')]
    .find((s) => s.textContent === 'Depth range')?.parentElement;
  const mask = es.getState().draft?.masks?.[idx] ?? {};
  window.__maskProbe = {
    rowFound: !!row,
    thumbCount: row?.querySelectorAll('[data-slot="slider-thumb"]').length ?? 0,
    display: row?.querySelector('span.font-mono')?.textContent ?? '',
    depthLo: mask.depthLo,
    depthHi: mask.depthHi,
  };
} else if (shot.startsWith('browse')) {
  // Browse latency probe: arrow-step through the folder at a human culling
  // pace and measure how long the render chip stays busy per step. On a
  // fully pre-rendered, unedited folder every number should be tens of ms —
  // anything in the seconds is THE stall. Variants: -hidpi forces a 4K-class
  // devicePixelRatio (fit crosses tile depth — the config from the
  // 2026-07-14 field log), -cull runs in cull mode, -zoomed at a numeric
  // (non-'fit') zoom state.
  if (shot.includes('hidpi')) {
    Object.defineProperty(window, 'devicePixelRatio', { value: 3, configurable: true });
  }
  ui().setMode(shot.includes('cull') ? 'cull' : 'develop');
  const es = mw.useEditSession;
  if (!shot.includes('cull')) await until(() => es.getState().draft != null);
  await sleep(1500);
  if (shot.includes('zoomed')) {
    ui().setLoupeZoom(0.4);
    await sleep(500);
  }
  const chip = () => document.querySelector('[data-testid="render-chip"]');
  const press = (key) =>
    window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
  const steps = [];
  for (let i = 0; i < 30; i++) {
    const t0 = performance.now();
    press('ArrowRight');
    await sleep(50); // give state a beat to flip busy
    while (chip()?.dataset.busy === 'true' && performance.now() - t0 < 20000) {
      await new Promise((r) => setTimeout(r, 25));
    }
    steps.push(Math.round(performance.now() - t0));
    await sleep(150); // culling pace
  }
  const sorted = [...steps].sort((a, b) => a - b);
  window.__maskProbe = {
    steps,
    median: sorted[Math.floor(sorted.length / 2)],
    p90: sorted[Math.floor(sorted.length * 0.9)],
    max: sorted[sorted.length - 1],
    over1s: steps.filter((s) => s > 1000).length,
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
} else if (shot === 'models') {
  // Downloaded-models inventory: seed the models dir first (models-verify.mjs
  // leaves three specs + one orphan behind), open Settings → Models, probe
  // the rows the live GetModelsInfo subscription rendered.
  ui().setSettingsOpen(true);
  await sleep(300);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'Models')?.click();
  await sleep(800);
  const dlg = document.querySelector('[role="dialog"]');
  const rows = [...dlg.querySelectorAll('.text-sm.font-medium')]
    .map((e) => e.textContent)
    .filter((t) => t !== 'Downloaded models');
  window.__modelsProbe = { rows, text: dlg.textContent.includes('Not used by this version') };
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
} else if (shot === 'neardup') {
  // Near-duplicate burst badges in the library grid: the fixture's identical
  // copies must all carry the ⧉ badge, and exactly one per group must be
  // highlighted as the sharpest frame.
  await until(() => document.querySelector('[data-testid="burst-badge"]'), 60000);
  const badges = [...document.querySelectorAll('[data-testid="burst-badge"]')];
  window.__neardupProbe = {
    badges: badges.length,
    best: badges.filter((b) => b.dataset.best).length,
    labels: [...new Set(badges.map((b) => b.textContent.trim()))],
  };
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
} else if (shot === 'subjects' || shot === 'subjectscan') {
  // Library toolbar's subject-scan control ("Subjects", beside "Soft"). Hide
  // the develop panel so the @container toolbar is wide enough to show labels.
  ui().setMode('library');
  ui().setView('grid');
  ui().setShowEditPanel(false);
  await sleep(400);
  const scanBtn = document.querySelector('[data-testid="subject-scan-button"]');
  if (shot === 'subjectscan') {
    // Open the folder-wide "analyze subjects & re-score focus" dialog.
    scanBtn?.click();
    await until(() => document.querySelector('[data-testid="subject-scan-dialog"]'), 5000);
  }
  window.__subjectProbe = {
    scanButton: !!scanBtn,
    label: scanBtn?.textContent?.trim() ?? null,
    dialogOpen: !!document.querySelector('[data-testid="subject-scan-dialog"]'),
    startLabel:
      [...document.querySelectorAll('[data-testid="subject-scan-start"]')][0]?.textContent?.trim() ??
      null,
  };
} else if (shot === 'cullundo') {
  // Flag/rating undo history: P → Ctrl+Z → Ctrl+⇧Z round-trips, stacked
  // rating undos, a burst judgement (⇧P) collapsing to ONE entry that
  // restores mixed priors, and Develop-mode routing. Drives the real keymap
  // via window keydown events.
  const ch = mw.useCullHistory;
  const press = (key, opts = {}) =>
    window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, ...opts }));
  const undoKey = () => press('z', { ctrlKey: true });
  const redoKey = () => press('z', { ctrlKey: true, shiftKey: true });
  // Overrides-first like the action layer: photoMeta refreshes on render.
  const metaOf = (id) => {
    const s = ui();
    const o = s.overrides.get(id) ?? {};
    const m = s.photoMeta.get(id) ?? { flag: 'none', rating: 0 };
    return { flag: o.flag ?? m.flag, rating: o.rating ?? m.rating };
  };
  // ⇧P needs burst grouping; the fixture's identical copies group once the
  // phash scan lands.
  await until(() => ui().burstMembers.size > 0, 60000);
  // Idempotence: clear any persisted flags/ratings, then start fresh.
  ui().selectAll(ui().visibleIds);
  press('u');
  press('0');
  await sleep(600);
  ch.setState({ stack: [], index: 0 });
  const ids = ui().visibleIds;
  const [a, b] = [ids[0], ids[1]];
  const stackLen = () => ch.getState().stack.length;

  // 1. Pick → undo → redo → undo (leaves A unflagged for the burst step).
  ui().focus(a);
  press('p');
  const pickApplied = metaOf(a).flag === 'pick' && stackLen() === 1;
  await sleep(250);
  undoKey();
  const pickUndone = metaOf(a).flag === 'none';
  await sleep(250); // sonner renders on the next React pass
  const undoToast = document.body.textContent.includes('Undid: Pick');
  redoKey();
  const pickRedone = metaOf(a).flag === 'pick';
  await sleep(250);
  undoKey();
  await sleep(250);

  // 2. Ratings stack: 3 then 5, two undos walk back to 0.
  press('3');
  await sleep(150);
  press('5');
  const rated = metaOf(a).rating === 5;
  await sleep(250);
  undoKey();
  const ratingBack1 = metaOf(a).rating === 3;
  await sleep(250);
  undoKey();
  const ratingBack0 = metaOf(a).rating === 0;
  await sleep(250);

  // 3. Burst judgement = ONE entry over mixed priors: B starts picked.
  ui().focus(b);
  press('p');
  await sleep(250);
  ui().focus(a);
  const lenBefore = stackLen();
  press('P', { shiftKey: true });
  const members = ui().burstMembers.get(a) ?? [];
  const judged =
    members.length > 1 &&
    metaOf(a).flag === 'pick' &&
    members.filter((id) => id !== a).every((id) => metaOf(id).flag === 'exclude');
  const oneEntry = stackLen() === lenBefore + 1;
  const label = ch.getState().stack[stackLen() - 1]?.label;
  await sleep(250);
  // Repeat press: everything already carries its target flag — no new entry.
  press('P', { shiftKey: true });
  const repeatNoop = stackLen() === lenBefore + 1;
  undoKey();
  const burstUndone =
    metaOf(a).flag === 'none' &&
    metaOf(b).flag === 'pick' &&
    members.filter((id) => id !== a && id !== b).every((id) => metaOf(id).flag === 'none');
  await sleep(250);
  const burstToast = document.body.textContent.includes('Undid: Best of burst');

  // 4. Routing: in Develop, Ctrl+Z drives the edit history, not this stack.
  ui().setMode('develop');
  await until(() => mw.useEditSession.getState().draft != null);
  const idxBefore = ch.getState().index;
  undoKey();
  const developRoutes = ch.getState().index === idxBefore;
  ui().setMode('library');
  await sleep(250);
  // Leave the fixture clean for repeat runs and the server-truth check.
  undoKey(); // B's pick
  await sleep(400);
  window.__cullUndoProbe = {
    pickApplied,
    pickUndone,
    undoToast,
    pickRedone,
    rated,
    ratingBack1,
    ratingBack0,
    judged,
    oneEntry,
    label,
    repeatNoop,
    burstUndone,
    burstToast,
    developRoutes,
  };
} else if (shot === 'heal') {
  // Retouch tool: activate healing, drop a spot in the middle of the frame,
  // and let the server pick its source so the overlay shows the destination
  // ring, the dashed source ring, and the connector.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200); // initial preview settles
  mw.esUpdate({ spots: [] }); // idempotence: drop any persisted spots
  mw.esCommit();
  await sleep(300);
  mw.esSetHealing(true);
  const idx = mw.esBeginSpot({ cx: 0.5, cy: 0.5, radius: 0.035, sx: 0.62, sy: 0.55, feather: 0.5 });
  await mw.esFinishSpot(idx);
  mw.esSetActiveSpot(idx);
  await until(() => mw.esPreviewSettled(), 30000).catch(() => {});
  await sleep(400);
  window.__healProbe = {
    healing: es.getState().healing,
    spotCount: es.getState().draft?.spots?.length ?? 0,
    overlay: !!document.querySelector('[data-testid="heal-overlay"]'),
    activeSpot: es.getState().activeSpot,
  };
} else if (shot === 'healbrush') {
  // Heal brush: paint a stroke-kind spot, let the server pick the source, and
  // show the painted region + translated source copy + panel Brush row.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200);
  mw.esUpdate({ spots: [] });
  mw.esCommit();
  await sleep(300);
  mw.esSetHealing(true);
  mw.esSetSpotTool('brush');
  const idx = mw.esBeginSpot({
    kind: 'stroke',
    cx: 0.5, cy: 0.5, radius: 0, sx: 0.62, sy: 0.55,
    strokes: [{ radius: 0.02, feather: 0.4, pts: [0.4, 0.48, 0.47, 0.52, 0.54, 0.5, 0.6, 0.53] }],
  });
  await mw.esFinishSpot(idx);
  mw.esSetActiveSpot(idx);
  await until(() => mw.esPreviewSettled(), 30000).catch(() => {});
  await sleep(400);
  const spot = es.getState().draft?.spots?.[idx];
  window.__healProbe = {
    healing: es.getState().healing,
    kind: spot?.kind,
    strokePts: spot?.strokes?.[0]?.pts?.length ?? 0,
    destMoved: !!spot && Math.hypot(spot.sx - spot.cx, spot.sy - spot.cy) > 0.02,
    overlay: !!document.querySelector('[data-testid="heal-overlay"]'),
    activeSpot: es.getState().activeSpot,
  };
} else if (shot === 'spotvis') {
  // Visualize spots: the high-pass dust view over the loupe while healing.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200);
  mw.esSetHealing(true);
  mw.esSetSpotVisualize(true);
  await until(() => !!document.querySelector('[data-testid="spot-visualize"]'), 10000).catch(() => {});
  await sleep(1500); // filter pass renders
  const canvas = document.querySelector('[data-testid="spot-visualize"]');
  window.__healProbe = {
    healing: es.getState().healing,
    visualize: es.getState().spotVisualize,
    canvas: !!canvas,
    canvasDrawn: !!canvas && canvas.width > 0,
  };
}
// Let previews decode, then wake the chrome (capture fires on resolve).
await sleep(3600);
window.dispatchEvent(new PointerEvent('pointermove', { clientX: 500, clientY: 300 }));
await sleep(400);
const probe =
  window.__autoCropProbe ??
  window.__cullUndoProbe ??
  window.__healProbe ??
  window.__subjectProbe ??
  window.__wmProbe ??
  window.__neardupProbe ??
  window.__modelsProbe ??
  window.__maskProbe ??
  window.__cropProbe ??
  window.__renderProbe ??
  window.__settleProbe ??
  window.__welcomeProbe ??
  window.__folderViewProbe;
return probe ? { shot, ...probe } : shot;
