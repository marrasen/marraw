// Runs inside the marraw renderer (see electron/main.cjs MARRAW_UITEST).
// Acceptance test for the reported repro: put a Background (inverted subject)
// mask on one photo, Ctrl+C, move to another photo and Ctrl+V — the pasted
// mask must render WITHOUT ever opening the Local tab.
//
// An AI mask is a recipe pointing at a per-photo model map, and a photo
// without that map on disk renders the mask as nothing at all. The map
// generation used to live in the Local panel's mask section, so it only ever
// ran while that tab was mounted: the pasted effect stayed invisible until
// the user happened to click Local. It now lives in editSession
// (esEnsureAIMaps), which every whole-draft path funnels through.
//
// The mask is pushed to a heavy exposure drop so "did it render?" is a
// measurable question: the pasted photo's preview has to get darker on its
// own, with the panel parked on Develop the whole time.
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

// meanLuma decodes the loupe's preview JPEG and averages it — the pixels the
// user is looking at, not the params behind them.
const meanLuma = async (url) => {
  const img = new Image();
  img.src = url;
  await img.decode();
  const c = document.createElement('canvas');
  c.width = 128;
  c.height = Math.max(1, Math.round((128 * img.height) / img.width));
  const ctx = c.getContext('2d');
  ctx.drawImage(img, 0, 0, c.width, c.height);
  const { data } = ctx.getImageData(0, 0, c.width, c.height);
  let sum = 0;
  for (let i = 0; i < data.length; i += 4) sum += 0.2126 * data[i] + 0.7152 * data[i + 1] + 0.0722 * data[i + 2];
  return sum / (data.length / 4);
};

try {
  const mw = await until(() => window.__marraw, 15000, '__marraw hooks');
  const ui = () => mw.useUIStore.getState();
  const es = () => mw.useEditSession.getState();
  // The settled sharp frame of the focused photo — a low-res drag frame or a
  // stale blob from the previous photo must never be what gets measured. Only
  // the live preview blob is measurable: /img renditions come from the daemon's
  // origin and would taint the canvas.
  const settledPreview = (photoId) => {
    const s = es();
    return s.preview && s.preview.photoId === photoId && s.rendering === 0 && mw.esPreviewSettled()
      ? s.preview.url
      : null;
  };
  // An unedited photo shows a cached /img rendition and has no preview blob at
  // all: a no-op commit schedules the sharp render that produces one.
  const forceRender = async (photoId) => {
    mw.esCommit();
    return until(() => settledPreview(photoId), 60000, 'settled preview blob');
  };
  // bumpImgBust's persisted nonce — advanced (only) when a map actually had to
  // be generated for the photo, so it says whether the inference really ran.
  const bustOf = (photoId) => {
    try {
      const raw = JSON.parse(localStorage.getItem('marraw.imgBust') || '[]');
      return (raw.find(([id]) => id === photoId) || [0, 0])[1];
    } catch {
      return 0;
    }
  };

  await until(() => ui().visibleIds.length > 0, 30000, 'photos loaded');
  const [src, dst] = ui().visibleIds;
  if (dst == null) throw new Error('need at least two photos in the fixture folder');

  ui().setMode('develop');
  ui().focus(src);
  await until(() => es().photoId === src && es().draft != null, 20000, 'edit session on the source photo');

  // Clean slate on both photos: a leftover mask from an aborted run would
  // make every measurement below meaningless.
  key('0', { ctrlKey: true });
  await sleep(1200);
  ui().focus(dst);
  await until(() => es().photoId === dst && es().draft != null, 20000, 'edit session on the target photo');
  key('0', { ctrlKey: true });
  await sleep(1200);
  ui().focus(src);
  await until(() => es().photoId === src && es().draft != null, 20000, 'back on the source photo');

  // --- source photo: Background mask, cranked down ---------------------------
  ui().setDevelopTab('masks');
  const bgBtn = await until(
    () => document.querySelector('[data-testid="ai-mask-background"]'),
    10000,
    'Background button',
  );
  bgBtn.click();
  // First run on this fixture actually infers (isnet on a full RAW decode).
  await until(() => (es().draft.masks ?? []).some((m) => m.aiKind === 'subject'), 120000, 'background mask added');
  R.backgroundMaskAdded = true;

  mw.esUpdateMask(0, { adjust: { expEV: -4 } });
  mw.esCommit();
  await sleep(300);
  await until(() => settledPreview(src), 60000, 'source photo settled with the mask');
  R.sourceMaskRenders = true;

  // Copy the settings, then park the panel back on Develop for the rest of
  // the run — the whole point is that Local is never mounted again.
  key('c', { ctrlKey: true });
  await until(() => ui().clipboard && (ui().clipboard.masks ?? []).some((m) => m.aiKind === 'subject'), 10000, 'settings copied');
  R.clipboardCarriesMask = true;
  ui().setDevelopTab('develop');
  await sleep(200);

  // --- target photo: paste, and never touch Local ---------------------------
  ui().focus(dst);
  await until(() => es().photoId === dst && es().draft != null, 20000, 'edit session on the target photo');
  const before = await meanLuma(await forceRender(dst));
  const bustBefore = bustOf(dst);

  key('v', { ctrlKey: true });
  await until(() => (es().draft.masks ?? []).some((m) => m.aiKind === 'subject'), 10000, 'mask pasted into the draft');
  R.pastedDraftHasMask = true;

  // The paste's own settle renders BEFORE the map exists — the mask is a
  // no-op then, and that maskless frame is the baseline to beat. Measuring it
  // rather than the pre-paste frame keeps the assertion independent of how
  // the two photos' base exposure compares: nothing but the mask coming alive
  // can darken it from here.
  let baseline = before;
  if (bustOf(dst) === bustBefore) {
    const url = await until(() => settledPreview(dst), 60000, 'target photo settled after the paste');
    if (bustOf(dst) === bustBefore) baseline = await meanLuma(url);
  }

  let after = baseline;
  const deadline = Date.now() + 180000;
  while (Date.now() < deadline && after >= baseline - 5) {
    await sleep(1000);
    const url = settledPreview(dst);
    if (url) after = await meanLuma(url);
  }
  R.pastedMaskRendersWithoutLocalTab =
    after < baseline - 5 ? true : `mean luma ${baseline.toFixed(1)} -> ${after.toFixed(1)} (expected a drop)`;
  R.mapGeneratedForTarget =
    bustOf(dst) > bustBefore ? true : `imgBust ${bustBefore} -> ${bustOf(dst)} (no map ran for the pasted photo)`;
  R.stayedOffLocalTab = ui().developTab === 'develop' ? true : `developTab=${ui().developTab}`;

  // Leave the fixture as found.
  ui().focus(dst);
  key('0', { ctrlKey: true });
  await sleep(800);
  ui().focus(src);
  await until(() => es().photoId === src, 10000, 'back on the source photo');
  key('0', { ctrlKey: true });
  await sleep(800);
  return R;
} catch (err) {
  R.fatal = String(err && err.stack ? err.stack : err);
  return R;
}
