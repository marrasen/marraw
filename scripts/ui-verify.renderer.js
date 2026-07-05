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
  const resetBtn = buttons().find((b) => b.textContent.trim() === 'Reset');
  if (resetBtn) {
    resetBtn.click();
    await until(() => es().draft && es().draft.expEV === 0, 5000, 'baseline reset');
  }
  key('0'); // clear rating
  await sleep(200);

  // --- E focuses exposure, +/- steps, preview blob arrives ---------------
  key('e');
  R.controlFocusE = es().activeControl === 'expEV';
  key('+'); key('+'); key('+'); key('+');
  R.plusSteps = Math.abs(es().draft.expEV - 0.2) < 1e-9;
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
  R.undo = es().draft.expEV === 0;
  key('y', { ctrlKey: true });
  await sleep(300);
  R.redo = Math.abs(es().draft.expEV - 0.2) < 1e-9;

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
  R.wbControls = ['As shot', 'Auto', 'Picked'].every((t) => buttons().some((b) => b.textContent.trim() === t));
  R.wbSliders = !!sliderRowByLabel('Temperature') && !!sliderRowByLabel('Tint');
  R.newSliders = !!sliderRowByLabel('Gamma') && !!sliderRowByLabel('Shadow slope') && !!sliderRowByLabel('Median passes');
  R.highlightButtons = ['Clip', 'Unclip', 'Blend', 'Rebuild'].every((t) => buttons().some((b) => b.textContent.trim() === t));

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
