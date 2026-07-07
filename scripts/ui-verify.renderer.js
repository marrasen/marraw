// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Drives the UI through keyboard/pointer events and the dev-only
// window.__marraw store hooks, returning a results object.
const R = {};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const until = async (fn, ms = 15000, what = 'condition') => {
  const t = Date.now();
  for (;;) {
    let v;
    try { v = fn(); } catch { v = null; }
    if (v) return v;
    if (Date.now() - t > ms) throw new Error(`timeout: ${what}`);
    await sleep(100);
  }
};
const key = (k, mods = {}) =>
  window.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, ...mods }));
const buttons = () => [...document.querySelectorAll('button')];
const stripImgs = () => [...document.querySelectorAll('[data-testid="filmstrip"] img')];
const sliderRowByLabel = (label) => {
  const span = [...document.querySelectorAll('span')].find((s) => s.textContent.trim().startsWith(label));
  return span ? span.closest('div')?.parentElement : null;
};

try {
  const mw = await until(() => window.__marraw, 15000, '__marraw hooks');
  const ui = () => mw.useUIStore.getState();
  const es = () => mw.useEditSession.getState();

  await until(() => ui().visibleIds.length > 0, 30000, 'photos loaded');
  R.photosLoaded = ui().visibleIds.length;

  // The control-presence checks need every collapsible edit group open; an
  // interactive session may have persisted some collapsed. The setter writes
  // the store optimistically, so it applies before the panel mounts.
  for (const g of ['crop', 'tone', 'presence', 'wb', 'color', 'effects', 'detail'])
    mw.setEditGroupOpen(g, true);

  // --- thumbnail-size slider renders at its designed width ------------------
  // Regression: width passed as className on the Slider root lost to the
  // root's own data-horizontal:w-full and the track collapsed to ~12px.
  const thumbSlider = document.querySelector('[title="Thumbnail size"] [data-slot="slider"]');
  const thumbW = thumbSlider ? thumbSlider.getBoundingClientRect().width : 0;
  R.thumbSliderWidth = thumbW >= 90 ? true : `width=${thumbW}px`;

  // --- ⌘K palette: theme commands flip the root class ----------------------
  const themeBefore = ui().theme;
  ui().setPaletteOpen(true);
  await until(() => buttons().some((b) => b.textContent.includes('Theme: Light')), 5000, 'palette theme commands');
  buttons().find((b) => b.textContent.includes('Theme: Light')).click();
  await until(() => document.documentElement.classList.contains('light'), 5000, 'light theme applied');
  R.paletteThemeLight = true;
  ui().setPaletteOpen(true);
  await until(() => buttons().some((b) => b.textContent.includes('Theme: Dark')), 5000, 'palette reopened');
  buttons().find((b) => b.textContent.includes('Theme: Dark')).click();
  await until(() => document.documentElement.classList.contains('dark'), 5000, 'dark theme restored');
  R.paletteThemeRestore = true;
  // Don't leak the probe's theme choice into interactive sessions (theme is
  // server-persisted now, so the palette clicks above really changed it).
  mw.setTheme(themeBefore);

  // --- keyboard focus + loupe -------------------------------------------
  key('ArrowRight');
  await until(() => ui().focusId != null, 5000, 'focus via arrow');
  key('Enter');
  await until(() => ui().view === 'loupe', 5000, 'loupe view');
  R.loupe = true;
  // Enter drops into the Cull confirm loupe (design handoff); the develop
  // control checks below run against the Develop cinema drawer instead.
  ui().setMode('develop');
  await sleep(300);
  await until(() => es().draft != null, 15000, 'edit session loaded');
  const photoA = ui().focusId;

  // Start from a clean baseline even if a previous aborted run left edits.
  // The clean draft may carry the seeded camera-mimic exposure (base_exp_ev),
  // so exposure checks below are relative to whatever the baseline reads.
  const resetBtn = buttons().find((b) => b.textContent.trim() === 'Reset');
  if (resetBtn) {
    resetBtn.click();
    await sleep(900); // reset round-trips, then reloads the seeded baseline
  }
  key('0'); // clear rating
  await sleep(200);
  const baseEV = es().draft.expEV;

  // --- E focuses exposure, +/- steps, preview blob arrives ---------------
  key('e');
  R.controlFocusE = es().activeControl === 'expEV';
  key('+'); key('+'); key('+'); key('+');
  R.plusSteps = Math.abs(es().draft.expEV - (baseEV + 0.2)) < 1e-9;
  await until(() => es().preview && es().preview.photoId === photoA, 20000, 'preview blob');
  R.previewBlob = true;
  key('Escape');
  R.escClearsControl = es().activeControl === null && ui().view === 'loupe';

  // --- histogram has pixels ----------------------------------------------
  await sleep(600);
  const hcanvas = document.querySelector('[data-testid="histogram"]');
  if (hcanvas) {
    const d = hcanvas.getContext('2d').getImageData(0, 0, hcanvas.width, hcanvas.height).data;
    R.histogram = d.some((v) => v > 0);
  } else {
    R.histogram = 'canvas missing';
  }

  // --- undo / redo --------------------------------------------------------
  await sleep(900); // let the keyboard-step commit debounce fire
  key('z', { ctrlKey: true });
  await sleep(300);
  R.undo = Math.abs(es().draft.expEV - baseEV) < 1e-9;
  key('y', { ctrlKey: true });
  await sleep(300);
  R.redo = Math.abs(es().draft.expEV - (baseEV + 0.2)) < 1e-9;

  // --- slider click-to-position -------------------------------------------
  const expRow = sliderRowByLabel('Exposure');
  const control = expRow?.querySelector('[data-slot="slider"] > div');
  if (control) {
    const rect = control.getBoundingClientRect();
    const x = rect.left + rect.width * 0.75;
    const y = rect.top + rect.height / 2;
    control.dispatchEvent(new PointerEvent('pointerdown', {
      bubbles: true, cancelable: true, button: 0, clientX: x, clientY: y, pointerId: 7, isPrimary: true,
    }));
    document.dispatchEvent(new PointerEvent('pointerup', {
      bubbles: true, cancelable: true, button: 0, clientX: x, clientY: y, pointerId: 7, isPrimary: true,
    }));
    await sleep(200);
    // -2..3 range: 75% ≈ +1.75
    R.sliderClickJumps = es().draft.expEV > 1.4 && es().draft.expEV < 2.1 ? true : `expEV=${es().draft.expEV}`;
  } else {
    R.sliderClickJumps = 'exposure slider not found';
  }

  // --- WB controls present -------------------------------------------------
  // Picked mode lives in the icon-only pipette button (identified by title).
  R.wbControls = ['As shot', 'Auto', 'Kelvin'].every((t) => buttons().some((b) => b.textContent.trim() === t)) &&
    buttons().some((b) => (b.title || '').toLowerCase().includes('white balance'));
  R.wbSliders = !!sliderRowByLabel('Temperature') && !!sliderRowByLabel('Tint');
  R.newSliders = !!sliderRowByLabel('Gamma') && !!sliderRowByLabel('Shadow slope') && !!sliderRowByLabel('Median passes');
  R.highlightButtons = ['Clip', 'Unclip', 'Blend', 'Rebuild'].every((t) => buttons().some((b) => b.textContent.trim() === t));

  // --- look-stage adjustment controls present ------------------------------
  R.toneSliders =
    !!sliderRowByLabel('Contrast') && !!sliderRowByLabel('Whites') && !!sliderRowByLabel('Blacks') &&
    !!sliderRowByLabel('Shadows') && !!sliderRowByLabel('Highlights');
  R.colorSliders =
    !!sliderRowByLabel('Saturation') && !!sliderRowByLabel('Vibrance') &&
    !!sliderRowByLabel('Shadow tint') && !!sliderRowByLabel('Vignette');
  R.detailControls =
    ['VNG', 'PPG', 'AHD', 'DHT'].every((t) => buttons().some((b) => b.textContent.trim() === t)) &&
    !!sliderRowByLabel('CA red/cyan');

  // --- C focuses contrast, stepping patches the draft ----------------------
  key('c');
  R.contrastFocus = es().activeControl === 'contrast';
  key('+');
  R.contrastSteps = Math.abs(es().draft.contrast - 0.02) < 1e-9;
  key('Escape');
  await sleep(900); // let the step commit debounce settle before moving on

  // --- hotkey into a closed group auto-opens it -----------------------------
  mw.setEditGroupOpen('tone', false);
  await sleep(150);
  key('e');
  await sleep(150);
  R.hotkeyOpensGroup =
    es().activeControl === 'expEV' && ui().editGroups.tone !== false && !!sliderRowByLabel('Exposure')
      ? true
      : `active=${es().activeControl} tone=${ui().editGroups.tone} row=${!!sliderRowByLabel('Exposure')}`;

  // --- Ctrl+↑/↓ skips controls inside closed groups --------------------------
  mw.setEditGroupOpen('presence', false);
  mw.useEditSession.setState({ activeControl: 'toneHighlights' });
  key('ArrowDown', { ctrlKey: true });
  R.ctrlDownSkipsClosed = es().activeControl === 'wbMode' ? true : `active=${es().activeControl}`;
  key('ArrowUp', { ctrlKey: true });
  R.ctrlUpSkipsClosed = es().activeControl === 'toneHighlights' ? true : `active=${es().activeControl}`;
  mw.setEditGroupOpen('presence', true);
  key('Escape');
  await sleep(150);

  // --- auto adjustments: buttons present, Ctrl+U family lands, undo reverts
  R.autoButtons =
    buttons().some((b) => (b.title || '').startsWith('Auto dynamics')) &&
    buttons().some((b) => (b.title || '').startsWith('Auto colours')) &&
    buttons().some((b) => (b.title || '').startsWith('Auto everything'));
  // Ctrl+U auto dynamics: the draft carries ~+1.75 EV from the slider click
  // above, so auto tone must land a different (pulled-back) state.
  const preAutoEV = es().draft.expEV;
  const preAutoJSON = JSON.stringify(es().draft);
  key('u', { ctrlKey: true });
  await until(() => JSON.stringify(es().draft) !== preAutoJSON, 20000, 'auto tone landed');
  R.autoTone =
    es().draft.expEV < preAutoEV && es().draft.wbMode !== 'auto' && es().draft.saturation === 0
      ? true
      : `expEV ${preAutoEV} -> ${es().draft.expEV}, wb=${es().draft.wbMode}, sat=${es().draft.saturation}`;
  await sleep(400);
  // Ctrl+Shift+U auto colours: switches WB to auto and computes vibrance.
  key('u', { ctrlKey: true, shiftKey: true });
  await until(() => es().draft.wbMode === 'auto', 20000, 'auto colours landed');
  R.autoColours = true;
  await sleep(400);
  key('z', { ctrlKey: true }); // one undo step reverts the whole auto
  await sleep(300);
  R.autoUndo = es().draft.wbMode !== 'auto' ? true : 'undo left wbMode=auto';
  await sleep(400);

  // --- Kelvin mode swaps the temperature slider -----------------------------
  const kelvinBtn = buttons().find((b) => b.textContent.trim() === 'Kelvin');
  kelvinBtn.click();
  await until(() => es().draft.wbMode === 'kelvin' && es().draft.wbKelvin === 5500, 5000, 'kelvin mode');
  R.kelvinMode = sliderRowByLabel('Temperature')?.textContent.includes('5500 K') ?? false;
  const asShotBtn = buttons().find((b) => b.textContent.trim() === 'As shot');
  asShotBtn.click();
  await until(() => es().draft.wbMode !== 'kelvin', 5000, 'back to as-shot');
  await sleep(400);

  // --- crop mode: overlay mounts, crop applies, dims propagate -------------
  R.cropControls = !!sliderRowByLabel('Straighten') &&
    buttons().some((b) => b.textContent.trim().startsWith('Crop'));
  key('r'); // toggle crop mode
  await until(() => es().cropping && ui().view === 'loupe', 5000, 'crop mode on');
  // The overlay (and the CSS-rotation preview) mounts once the flat
  // crop-stripped render arrives — the CropBar appears instantly.
  await until(() => document.querySelector('[data-testid="crop-overlay"]'), 15000, 'crop overlay up');
  R.cropOverlay = !!document.querySelector('[data-testid="crop-overlay"]') &&
    ['Free', '1:1', '16:9', 'Done'].every((t) => buttons().some((b) => b.textContent.trim() === t));
  // Straighten previews as a live client-side rotation (no backend render):
  // the loupe image carries a rotate() transform while cropping.
  mw.esUpdate({ cropAngle: 7 });
  await sleep(80);
  R.straightenCss = [...document.querySelectorAll('img')].some((im) => (im.style.transform || '').includes('rotate'));
  // A near-full crop at an angle must auto-shrink to stay clear of the black
  // wedge the rotation exposes.
  mw.esUpdate({ cropX: 0.05, cropY: 0.05, cropW: 0.9, cropH: 0.9 });
  mw.esUpdate({ cropAngle: 14 });
  // Convergence takes two animation frames (draft flush → CropOverlay fit
  // effect → second flush); the window is generous because rAF can be
  // briefly starved under harness load.
  await until(() => es().draft.cropW < 0.9, 8000, 'crop fit to angle').catch(() => {});
  R.cropFitsAngle =
    es().draft.cropW < 0.9 && es().draft.cropW > 0.3
      ? true
      : `cropW=${es().draft.cropW} angle=${es().draft.cropAngle} overlay=${!!document.querySelector('[data-testid="crop-overlay"]')} flat=${es().preview?.flat}`;
  // Apply a half-frame crop through the dev bridge, then exit crop mode.
  mw.esUpdate({ cropX: 0.2, cropY: 0.2, cropW: 0.5, cropH: 0.5, cropAngle: 0 });
  await sleep(50);
  key('Enter'); // apply crop
  await until(() => !es().cropping, 5000, 'crop applied');
  R.cropApplied = es().draft.cropW === 0.5 && es().draft.cropH === 0.5 && es().draft.cropX === 0.2;
  // The rendered preview now has the cropped aspect ratio (0.5·W / 0.5·H of a
  // 3:2-ish frame ≈ the same ratio) and the overlay is gone.
  R.cropOverlayGone = !document.querySelector('[data-testid="crop-overlay"]');
  await sleep(300);

  // --- crop mode survives a straighten commit at tile depth -----------------
  // Regression (fixed 2026-07-07): a mid-crop commit bumps editHash; at tile
  // depth the loupe used to evict the flat preview and show the committed
  // crop-baked rendition stretched into the full-frame box and rotated AGAIN
  // ("base image looks cropped before rotating").
  ui().setLoupeZoom(1); // 42MP at 100% is past pyramid depth on any display
  key('r');
  await until(() => es().cropping, 5000, 'crop mode re-entered');
  await until(
    () => es().preview && es().preview.flat === true && es().preview.photoId === ui().focusId,
    20000,
    'flat crop-mode preview',
  );
  await until(() => document.querySelector('[data-testid="crop-overlay"]'), 5000, 'overlay over flat frame');
  mw.esUpdate({ cropAngle: 5 });
  await sleep(300); // angle lands + CropOverlay's auto-fit pushes back
  mw.esCommit();
  await sleep(2500); // PhotoPatchEvent lands (editHash bump — the old eviction trigger)
  const cropBase = document.querySelector('.overflow-auto img');
  R.cropCommitKeepsFlat =
    es().cropping && es().preview && es().preview.flat === true && (cropBase?.src ?? '').startsWith('blob:')
      ? true
      : `cropping=${es().cropping} flat=${es().preview?.flat} src=${(cropBase?.src ?? '').slice(0, 30)}`;
  R.cropCommitKeepsRotation = (cropBase?.style.transform ?? '').includes('rotate(5')
    ? true
    : (cropBase?.style.transform || '(none)');

  // --- crop drags slide along the tilted edge instead of freezing -----------
  // A small interior rect, then one big pointer jump far past the rotated
  // frame's boundary. The old code rejected any move whose corners left
  // coverage — a big jump left the rect exactly at its start; now it slides
  // to the largest covered position (both axes advance).
  mw.esUpdate({ cropX: 0.4, cropY: 0.4, cropW: 0.2, cropH: 0.2 });
  await until(() => es().draft.cropX === 0.4, 3000, 'test rect in draft');
  await sleep(250); // rect resyncs into the overlay
  const ovl = document.querySelector('[data-testid="crop-overlay"]');
  const rectEl = ovl.querySelector(':scope > div.border');
  const ovlRect = ovl.getBoundingClientRect();
  const cpt = (el, type, fx, fy) =>
    el.dispatchEvent(new PointerEvent(type, {
      bubbles: true, cancelable: true, button: 0, buttons: 1, pointerId: 11, isPrimary: true,
      clientX: ovlRect.left + fx * ovlRect.width, clientY: ovlRect.top + fy * ovlRect.height,
    }));
  const posOf = () => [parseFloat(rectEl.style.left), parseFloat(rectEl.style.top)]; // % of frame
  const p0 = posOf();
  cpt(rectEl, 'pointerdown', 0.5, 0.5);
  cpt(rectEl, 'pointermove', 0.02, 0.05); // toward the top-left wedge in one jump
  cpt(rectEl, 'pointerup', 0.02, 0.05);
  await sleep(150);
  const p1 = posOf();
  // The slide clamp is per-axis sequential (x gets the budget first), so a
  // drag into a wedge may exhaust x and leave y pinned — assert substantial
  // total displacement (the old code left the rect exactly at its start).
  R.cropDragSlides = p0[0] - p1[0] + (p0[1] - p1[1]) > 10 ? true : `(${p0}) -> (${p1})`;

  // A resize handle dragged far outside grows the rect to the largest covered
  // size instead of sticking at its start size.
  mw.esUpdate({ cropX: 0.4, cropY: 0.4, cropW: 0.2, cropH: 0.2 });
  await sleep(250);
  const seHandle = rectEl.querySelector('.-bottom-2.-right-2');
  const w0 = parseFloat(rectEl.style.width);
  cpt(seHandle, 'pointerdown', 0.6, 0.6);
  cpt(seHandle, 'pointermove', 0.98, 0.98);
  cpt(seHandle, 'pointerup', 0.98, 0.98);
  await sleep(150);
  const w1 = parseFloat(rectEl.style.width);
  R.cropResizeSlides = w1 > w0 + 5 ? true : `w ${w0}% -> ${w1}%`;

  key('Escape'); // exit crop (commits; Reset below cleans everything)
  await until(() => !es().cropping, 5000, 'crop exited');
  ui().setLoupeZoom('fit');
  await sleep(400);

  // Reset back to neutral (persisted) so later checks and the DB start clean.
  buttons().find((b) => b.textContent.trim() === 'Reset')?.click();
  await until(() => es().draft.cropW === 0, 5000, 'crop reset');
  await sleep(200);

  // --- filmstrip: rating badge + multi-select ------------------------------
  key('3');
  await until(() => document.querySelector('[data-testid="strip-rating"]'), 5000, 'filmstrip rating badge');
  R.filmstripBadge = true;
  const thumbButtons = [...document.querySelectorAll('[data-testid="filmstrip"] button')];
  if (thumbButtons.length >= 3) {
    thumbButtons[2].dispatchEvent(new MouseEvent('click', { bubbles: true, ctrlKey: true }));
    await sleep(200);
    R.filmstripMultiSelect = ui().selection.size === 2;
    R.batchBanner = [...document.querySelectorAll('span')].some((s) => s.textContent.includes('2 photos selected'));
    // back to single selection
    thumbButtons[0].dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await sleep(200);
  }

  // --- reset photo A edits (cleanup for repeat runs) -----------------------
  ui().focus(photoA);
  await until(() => es().photoId === photoA && es().draft != null, 5000, 'session back on A');

  // --- paste: filmstrip thumbnail must not break ---------------------------
  key('c', { ctrlKey: true });
  await until(() => ui().clipboard != null, 5000, 'clipboard');
  key('ArrowRight');
  await until(() => ui().focusId !== photoA && es().photoId === ui().focusId && es().draft != null, 8000, 'next photo session');
  const photoB = ui().focusId;
  key('v', { ctrlKey: true });
  await sleep(2500); // let the patch land and the filmstrip img reload
  await until(
    () => stripImgs().length > 0 && stripImgs().every((img) => !img.complete || img.naturalWidth > 0),
    20000,
    'filmstrip thumbs after paste',
  );
  R.pasteThumbsOk = true;

  // --- unculled filter keeps position on exclude ---------------------------
  // Esc first releases a focused control / picker, then leaves the loupe.
  for (let i = 0; i < 3 && ui().view !== 'grid'; i++) {
    key('Escape');
    await sleep(150);
  }
  await until(() => ui().view === 'grid', 5000, 'back to grid');
  ui().setFilters({ flagFilter: 'not-excluded' });
  await sleep(300);
  const ids0 = ui().visibleIds.slice();
  const target = ids0[9];
  ui().focus(target);
  key('x');
  await until(() => !ui().visibleIds.includes(target), 5000, 'excluded photo removed');
  R.positionKept = ui().focusId === ui().visibleIds[9] ? true : `focus at ${ui().visibleIds.indexOf(ui().focusId)}`;
  // cleanup: unflag it again
  ui().setFilters({ flagFilter: 'all' });
  await sleep(200);
  ui().focus(target);
  key('u');
  await sleep(300);

  // --- selection bar takes over the filter row on multi-select --------------
  ui().focus(ids0[0]);
  ui().focus(ids0[1], { toggle: true });
  R.selectionBar = !!(await until(
    () =>
      buttons().find((b) => b.textContent.trim() === 'Paste settings') &&
      buttons().find((b) => b.textContent.trim() === 'Restore original') &&
      [...document.querySelectorAll('span')].some((s) => s.textContent.trim() === 'selected'),
    5000,
    'selection bar',
  ));
  key('Escape'); // Esc clears the selection
  await sleep(200);
  R.selectionEscClears = ui().selection.size <= 1;
  ui().focus(ids0[0]);

  // --- export dialog: segmented labels + default dir ------------------------
  key('e', { ctrlKey: true });
  await until(() => document.querySelector('input[placeholder*="Destination"]'), 5000, 'export dialog');
  const segTexts = [...document.querySelectorAll('[role="radio"]')].map((b) => b.textContent.trim());
  R.exportSelectLabels =
    ['JPEG', '16-bit TIFF', 'Full res', 'Long edge', 'sRGB', 'Adobe RGB', 'ProPhoto'].every((t) => segTexts.includes(t))
      ? true
      : segTexts;
  const destInput = document.querySelector('input[placeholder*="Destination"]');
  R.exportDefaultDir = destInput ? destInput.value : 'input missing';
  ui().setExportOpen(false);
  await sleep(300);

  // --- task tray exists ------------------------------------------------------
  R.taskTray = !!document.querySelector('[data-testid="task-tray"]');

  // --- loupe 1:1: tiles sharpen, switches bridge via 2048, neighbors prefetch
  // Own try/catch: a timeout here (e.g. a slow cold full render) must not
  // skip the edit-state cleanup below.
  try {
    const ids = ui().visibleIds;
    const i0 = Math.min(30, ids.length - 3);
    const [t1, t2, t3] = [ids[i0], ids[i0 + 1], ids[i0 + 2]];
    ui().focus(t1);
    key('Enter');
    await until(() => ui().view === 'loupe' && ui().focusId === t1, 5000, '1:1 loupe');
    // First img in the box is the 2048 underlay; tiles follow it.
    const underlaySrc = () => document.querySelector('.overflow-auto img')?.src ?? '';
    const tileLoaded = (id) =>
      [...document.querySelectorAll(`img[src*="/img/${id}/tile/"]`)].some((im) => im.complete && im.naturalWidth > 0);
    performance.clearResourceTimings();
    ui().setLoupeZoom(1);
    await until(() => tileLoaded(t1), 60000, 'tiles on screen');
    // Switching photos at 1:1 must show the next photo promptly (its 2048
    // underlay bridges) instead of holding the previous photo through a
    // full-resolution render.
    const tSwitch = performance.now();
    ui().focus(t2);
    await until(() => underlaySrc().includes(`/img/${t2}/2048`) || tileLoaded(t2), 60000, 'photo switch bridged');
    const bridgeMs = Math.round(performance.now() - tSwitch);
    R.loupe11Bridge = bridgeMs < 1500 ? true : `bridged after ${bridgeMs}ms`;
    await until(() => tileLoaded(t2), 60000, 'tiles after photo switch');
    // The photo ahead was prefetched: a tile request for it fired without it
    // ever being shown.
    await until(
      () => performance.getEntriesByType('resource').some((e) => e.name.includes(`/img/${t3}/tile/`)),
      60000,
      'neighbor tiles prefetched',
    );
    R.loupe11Prefetch = true;

    // --- pan/zoom interactions -----------------------------------------
    const pane = document.querySelector('.overflow-auto');
    R.loupeNoScrollbar = getComputedStyle(pane).scrollbarWidth === 'none';

    // Drag pans the zoomed image.
    const rect = pane.getBoundingClientRect();
    const [sx, sy] = [pane.scrollLeft, pane.scrollTop];
    const pt = (type, x, y) =>
      pane.dispatchEvent(new PointerEvent(type, {
        bubbles: true, cancelable: true, button: 0, buttons: 1, pointerId: 9, isPrimary: true,
        clientX: rect.left + x, clientY: rect.top + y,
      }));
    pt('pointerdown', 400, 300);
    pt('pointermove', 320, 250);
    pt('pointerup', 320, 250);
    await sleep(100);
    // Scroll positions snap to device pixels, so allow a ~1px tolerance.
    R.loupeDragPan =
      Math.abs(pane.scrollLeft - (sx + 80)) <= 1 && Math.abs(pane.scrollTop - (sy + 50)) <= 1
        ? true
        : `scroll ${sx},${sy} -> ${pane.scrollLeft},${pane.scrollTop}`;

    // Ctrl+wheel zooms toward the cursor: the image point under it stays put.
    // The image box sits centered in a pan-slack wrapper (see LoupeView), so
    // its offset in scroll coordinates is half the wrapper/box size delta.
    const wrapEl = pane.firstElementChild;
    const boxEl = wrapEl.firstElementChild;
    const imgPxAt = (x, y) => {
      const z = ui().loupeZoom;
      const offX = (parseFloat(wrapEl.style.width) - parseFloat(boxEl.style.width)) / 2;
      const offY = (parseFloat(wrapEl.style.height) - parseFloat(boxEl.style.height)) / 2;
      return [(pane.scrollLeft + x - offX) / z, (pane.scrollTop + y - offY) / z];
    };
    const anchorBefore = imgPxAt(400, 300);
    pane.dispatchEvent(new WheelEvent('wheel', {
      bubbles: true, cancelable: true, ctrlKey: true, deltaY: -300,
      clientX: rect.left + 400, clientY: rect.top + 300,
    }));
    await sleep(400); // let the zoom tween (~160ms) fully settle
    const anchorAfter = imgPxAt(400, 300);
    const drift = Math.hypot(anchorAfter[0] - anchorBefore[0], anchorAfter[1] - anchorBefore[1]);
    R.loupeWheelAnchor =
      ui().loupeZoom > 1.05 && drift < 2 ? true : `zoom ${ui().loupeZoom}, drift ${drift.toFixed(1)}px`;

    // After a zoom change, mounted tiles must render at the new scale — a
    // stale (memoization-frozen) tile layer shows content at the wrong
    // magnification and stops covering the image.
    await sleep(500);
    const zt = ui().loupeZoom;
    const fullTile = [...document.querySelectorAll(`img[src*="/img/${t2}/tile/"]`)].find(
      (t) => t.complete && t.naturalWidth === 1024 && t.getBoundingClientRect().width > 0,
    );
    const tw = fullTile ? fullTile.getBoundingClientRect().width : 0;
    R.loupe11TileScale = fullTile && Math.abs(tw - 1024 * zt) < 3 ? true : `zoom ${zt}, tile width ${tw.toFixed(1)}`;

    // Space toggles 1:1 <-> fit.
    key(' ');
    await sleep(100);
    const spaceToFit = ui().loupeZoom === 'fit';
    key(' ');
    await sleep(100);
    R.loupeSpaceToggle = spaceToFit && ui().loupeZoom === 1 ? true : `fit=${spaceToFit}, then ${ui().loupeZoom}`;

    ui().setLoupeZoom('fit');

    // --- re-clicking the active Fit button recenters a panned photo -------
    await sleep(400); // zoom tween + pan-ratio restore settle
    const centered = () =>
      Math.abs(pane.scrollLeft - (pane.scrollWidth - pane.clientWidth) / 2) <= 1 &&
      Math.abs(pane.scrollTop - (pane.scrollHeight - pane.clientHeight) / 2) <= 1;
    pt('pointerdown', 400, 300);
    pt('pointermove', 250, 200);
    pt('pointerup', 250, 200);
    await sleep(100);
    const offCenter = !centered();
    buttons().find((b) => b.textContent.trim() === 'Fit')?.click();
    await sleep(150);
    R.fitReclickCenters =
      offCenter && centered()
        ? true
        : `offCenter=${offCenter} scroll=${pane.scrollLeft},${pane.scrollTop}`;
  } catch (err) {
    R.loupe11Bridge = R.loupe11Bridge ?? String(err);
    R.loupe11Prefetch = R.loupe11Prefetch ?? String(err);
    R.loupeDragPan = R.loupeDragPan ?? String(err);
    R.loupeWheelAnchor = R.loupeWheelAnchor ?? String(err);
    R.loupe11TileScale = R.loupe11TileScale ?? String(err);
    R.loupeSpaceToggle = R.loupeSpaceToggle ?? String(err);
    R.loupeNoScrollbar = R.loupeNoScrollbar ?? String(err);
    R.fitReclickCenters = R.fitReclickCenters ?? String(err);
  }
  for (let i = 0; i < 3 && ui().view !== 'grid'; i++) {
    key('Escape');
    await sleep(150);
  }

  // --- feedback round: Esc steps modes, + steps from fit, ⇧+arrows pan,
  // quick dials come from the full control catalog ---------------------------
  try {
    mw.useEditSession.setState({ activeControl: null });

    // Esc in Develop steps back to Cull, then Library (mirrors Enter).
    ui().focus(photoA);
    ui().setMode('develop');
    await until(() => es().photoId === photoA && es().draft != null, 8000, 'develop session for esc check');
    key('Escape');
    R.escDevelopToCull = ui().mode === 'cull' ? true : `mode=${ui().mode}`;
    key('Escape');
    R.escCullToLibrary = ui().mode === 'library' ? true : `mode=${ui().mode}`;

    // '+' in fit zooms one step out of the actual fit scale, not to ~1:1.
    ui().setMode('cull');
    await until(() => ui().view === 'loupe', 5000, 'cull loupe for zoom check');
    ui().setLoupeZoom('fit');
    await sleep(400); // zoom tween + fit-scale mirror settle
    const fs = ui().loupeFitScale;
    key('+');
    R.plusFromFitSteps =
      fs < 0.95 && Math.abs(ui().loupeZoom - fs * 1.25) < 1e-6
        ? true
        : `fitScale=${fs}, zoom=${ui().loupeZoom}`;

    // ⇧+arrows pan the loupe by 20% of the viewport, without touching the
    // selection (extension stays grid/contact-sheet behavior).
    ui().setLoupeZoom(1);
    await sleep(400);
    const pane2 = document.querySelector('.overflow-auto');
    const [px, py] = [pane2.scrollLeft, pane2.scrollTop];
    const selBefore = ui().selection.size;
    key('ArrowRight', { shiftKey: true });
    key('ArrowDown', { shiftKey: true });
    await sleep(150);
    const [dxp, dyp] = [pane2.scrollLeft - px, pane2.scrollTop - py];
    R.shiftArrowPans =
      Math.abs(dxp - pane2.clientWidth * 0.2) <= 2 && Math.abs(dyp - pane2.clientHeight * 0.2) <= 2
        ? true
        : `d=${dxp},${dyp} viewport=${pane2.clientWidth}x${pane2.clientHeight}`;
    R.shiftArrowKeepsSelection = ui().selection.size === selBefore ? true : `selection=${ui().selection.size}`;
    ui().setLoupeZoom('fit');

    // Quick dials from the expanded catalog: a spec-backed numeric dial
    // (gamma, stored 0 = neutral 2.222) and a cycle chip (WB mode) render in
    // the Develop dock and drive the draft through the spec's get/set.
    const dialsBefore = ui().quickDials;
    mw.useUIStore.setState({ quickDials: ['gamma', 'wbMode'] });
    ui().setMode('develop');
    await until(() => es().photoId === photoA && es().draft != null, 8000, 'develop session for dial check');
    const miniByLabel = (label) =>
      [...document.querySelectorAll('[class*="w-[82px]"]')].find(
        (el) => el.querySelector('span')?.textContent.trim() === label,
      );
    const gammaMini = await until(() => miniByLabel('Gamma'), 5000, 'gamma quick dial');
    const gammaShown = gammaMini.querySelectorAll('span')[1]?.textContent.trim();
    R.quickDialGamma = gammaShown === '2.22' ? true : `display=${gammaShown}`;
    const cycleBtn = miniByLabel('WB mode')?.querySelector('button');
    if (cycleBtn && cycleBtn.textContent.trim() === 'As shot') {
      cycleBtn.click();
      await sleep(300);
      R.quickDialCycle =
        (es().draft.wbMode || 'camera') === 'auto' ? true : `wbMode=${es().draft.wbMode}`;
    } else {
      R.quickDialCycle = `WB mode chip: ${cycleBtn ? cycleBtn.textContent.trim() : 'not found'}`;
    }
    mw.useUIStore.setState({ quickDials: dialsBefore });
  } catch (err) {
    for (const name of [
      'escDevelopToCull', 'escCullToLibrary', 'plusFromFitSteps',
      'shiftArrowPans', 'shiftArrowKeepsSelection', 'quickDialGamma', 'quickDialCycle',
    ])
      R[name] = R[name] ?? String(err);
  }

  // --- cleanup: reset edits on A and B, clear rating -------------------------
  for (const id of [photoA, photoB]) {
    ui().focus(id);
    await until(() => es().photoId === id && es().draft != null, 5000, 'cleanup session');
    const reset = buttons().find((b) => b.textContent.trim() === 'Reset');
    if (reset) reset.click();
    await sleep(300);
  }
  ui().focus(photoA);
  key('0');
  await sleep(300);

  return R;
} catch (err) {
  R.fatal = String(err && err.stack ? err.stack : err);
  return R;
}
