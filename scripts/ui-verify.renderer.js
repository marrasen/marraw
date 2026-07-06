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
const stripImgs = () => [...document.querySelectorAll('div.h-24 img')];
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

  // --- keyboard focus + loupe -------------------------------------------
  key('ArrowRight');
  await until(() => ui().focusId != null, 5000, 'focus via arrow');
  key('Enter');
  await until(() => ui().view === 'loupe', 5000, 'loupe view');
  R.loupe = true;
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
  R.wbControls = ['As shot', 'Auto', 'Kelvin', 'Picked'].every((t) => buttons().some((b) => b.textContent.trim() === t));
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
  R.cropOverlay = !!document.querySelector('[data-testid="crop-overlay"]') &&
    ['Free', '1:1', '16:9', 'Done'].every((t) => buttons().some((b) => b.textContent.trim() === t));
  // Apply a half-frame crop through the dev bridge, then exit crop mode.
  mw.esUpdate({ cropX: 0.2, cropY: 0.2, cropW: 0.5, cropH: 0.5 });
  await sleep(50);
  key('Enter'); // apply crop
  await until(() => !es().cropping, 5000, 'crop applied');
  R.cropApplied = es().draft.cropW === 0.5 && es().draft.cropH === 0.5 && es().draft.cropX === 0.2;
  // The rendered preview now has the cropped aspect ratio (0.5·W / 0.5·H of a
  // 3:2-ish frame ≈ the same ratio) and the overlay is gone.
  R.cropOverlayGone = !document.querySelector('[data-testid="crop-overlay"]');
  await sleep(300);
  // Reset back to neutral (persisted) so later checks and the DB start clean.
  buttons().find((b) => b.textContent.trim() === 'Reset')?.click();
  await until(() => es().draft.cropW === 0, 5000, 'crop reset');
  await sleep(200);

  // --- filmstrip: rating badge + multi-select ------------------------------
  key('3');
  await until(() => document.querySelector('[data-testid="strip-rating"]'), 5000, 'filmstrip rating badge');
  R.filmstripBadge = true;
  const thumbButtons = [...document.querySelectorAll('div.h-24 button')];
  if (thumbButtons.length >= 3) {
    thumbButtons[2].dispatchEvent(new MouseEvent('click', { bubbles: true, ctrlKey: true }));
    await sleep(200);
    R.filmstripMultiSelect = ui().selection.size === 2;
    R.batchBanner = [...document.querySelectorAll('span')].some((s) => s.textContent.includes('applies to 2 photos'));
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

  // --- selection bar (batch delete/export buttons) --------------------------
  ui().focus(ids0[0]);
  R.selectionBar = !!(await until(
    () => buttons().find((b) => b.textContent.trim() === 'Delete') && buttons().find((b) => b.textContent.includes('Export') && b.closest('[class*="absolute"]')),
    5000,
    'selection bar',
  ));

  // --- export dialog: labels + default dir ----------------------------------
  key('e', { ctrlKey: true });
  await until(() => document.querySelector('[data-slot="select-value"]'), 5000, 'export dialog');
  const values = [...document.querySelectorAll('[data-slot="select-value"]')].map((e) => e.textContent.trim());
  R.exportSelectLabels = values.includes('JPEG') && values.includes('Full size') ? true : values;
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
    const boxEl = pane.firstElementChild;
    const imgPxAt = (x, y) => {
      const z = ui().loupeZoom;
      const offX = Math.max(0, (pane.clientWidth - parseFloat(boxEl.style.width)) / 2);
      const offY = Math.max(0, (pane.clientHeight - parseFloat(boxEl.style.height)) / 2);
      return [(pane.scrollLeft + x - offX) / z, (pane.scrollTop + y - offY) / z];
    };
    const anchorBefore = imgPxAt(400, 300);
    pane.dispatchEvent(new WheelEvent('wheel', {
      bubbles: true, cancelable: true, ctrlKey: true, deltaY: -300,
      clientX: rect.left + 400, clientY: rect.top + 300,
    }));
    await sleep(200);
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
  } catch (err) {
    R.loupe11Bridge = R.loupe11Bridge ?? String(err);
    R.loupe11Prefetch = R.loupe11Prefetch ?? String(err);
    R.loupeDragPan = R.loupeDragPan ?? String(err);
    R.loupeWheelAnchor = R.loupeWheelAnchor ?? String(err);
    R.loupe11TileScale = R.loupe11TileScale ?? String(err);
    R.loupeSpaceToggle = R.loupeSpaceToggle ?? String(err);
    R.loupeNoScrollbar = R.loupeNoScrollbar ?? String(err);
  }
  for (let i = 0; i < 3 && ui().view !== 'grid'; i++) {
    key('Escape');
    await sleep(150);
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
