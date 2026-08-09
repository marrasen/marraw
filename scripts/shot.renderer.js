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
const shot = new URLSearchParams(location.search).get('shot') || 'cull';

// ?shotFocus=<n> aims the capture at the n-th visible frame (capture order)
// instead of the default 7th — used to keep chosen subjects in the frame.
// ?shotGap=<min> overrides the time-gap grouping for the capture session.
const params = new URLSearchParams(location.search);
const focusIdx = Number(params.get('shotFocus') ?? 6);
// A dev profile whose stored lastSeenVersion is old raises the What's-new
// dialog over whatever surface is being shot. Dismiss it everywhere except
// the two surfaces that are about it.
if (shot !== 'welcome' && shot !== 'restore') {
  await sleep(400);
  document.querySelector('[data-testid="whats-new-dismiss"]')?.click();
}

// `restore` is launched with no folder requested (MARRAW_SHOT_NO_OPEN) — it
// is startup itself under test, and one of its cases never opens a folder at
// all, so it skips the preamble that waits for photos.
if (shot !== 'restore') {
  await until(() => ui().visibleIds.length > 0);
  if (params.get('shotGap')) {
    mw.setGapMinutes(Number(params.get('shotGap')));
    await sleep(800); // server write + regroup round-trip
  }
  ui().focus(ui().visibleIds[focusIdx] ?? ui().visibleIds[6] ?? ui().visibleIds[0]);
  await sleep(300);
}
if (shot === 'cull') {
  ui().setMode('cull');
} else if (shot === 'sheet') {
  ui().setMode('cull');
  await sleep(300);
  ui().setContactSheet(true);
} else if (shot === 'develop') {
  ui().setMode('develop');
} else if (shot === 'original' || shot === 'original-released') {
  // Hold-to-compare: the dock's Original button and \ both show the photo's
  // BASE rendition over the developed one, and BOTH must let go. A crop is
  // part of the edit so the layer's letterboxing (the original is a different
  // shape than the developed frame) is exercised, not just its pixels.
  // `original` captures mid-hold; `original-released` captures after the
  // release, where the same edit must be back on screen.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200); // initial preview settles

  const btn = [...document.querySelectorAll('button')].find(
    (b) => b.textContent.trim() === 'Original',
  );
  const press = (key, type = 'keydown') =>
    window.dispatchEvent(new KeyboardEvent(type, { key, bubbles: true }));
  const point = (type) =>
    btn?.dispatchEvent(new PointerEvent(type, { bubbles: true, pointerId: 1, isPrimary: true }));
  const layer = () => document.querySelector('[data-testid="original-layer"] img');
  const lumaOf = async (src) => {
    const blob = typeof src === 'string' ? await (await fetch(src)).blob() : src;
    // Both axes pinned: the two frames come from different pyramid levels, and
    // a width-only resize leaves them different heights (uncomparable arrays).
    const bmp = await createImageBitmap(blob, { resizeWidth: 64, resizeHeight: 64 });
    const c = document.createElement('canvas');
    c.width = bmp.width;
    c.height = bmp.height;
    const ctx = c.getContext('2d');
    ctx.drawImage(bmp, 0, 0);
    const d = ctx.getImageData(0, 0, bmp.width, bmp.height).data;
    return d;
  };
  const meanLuma = (d) => {
    let sum = 0;
    for (let i = 0; i < d.length; i += 4) sum += (d[i] * 299 + d[i + 1] * 587 + d[i + 2] * 114) / 1000;
    return Math.round(sum / (d.length / 4));
  };
  // Mean absolute difference over the same field of view. Sturdier than
  // comparing means: the fixture's seeded base exposure can leave the edited
  // frame no brighter overall even though every pixel moved.
  const meanDiff = (a, b) => {
    if (a.length !== b.length) return null;
    let sum = 0;
    for (let i = 0; i < a.length; i += 4) {
      sum += (Math.abs(a[i] - b[i]) + Math.abs(a[i + 1] - b[i + 1]) + Math.abs(a[i + 2] - b[i + 2])) / 3;
    }
    return Math.round(sum / (a.length / 4));
  };

  // Pass 0 — the key handoff. Backspace used to also delete the selected
  // retouch spot; that moved to Delete alone so the hold owns the key. With
  // the heal tool up and a spot chosen, Backspace must leave the spot standing
  // (and still hold) while Delete removes it.
  mw.esUpdate({ spots: [] }); // idempotence: drop any spots a previous run left
  mw.esCommit();
  await sleep(300);
  mw.esSetHealing(true);
  const spot = mw.esBeginSpot({ cx: 0.5, cy: 0.5, radius: 0.035, sx: 0.62, sy: 0.55, feather: 0.5 });
  await mw.esFinishSpot(spot);
  mw.esSetActiveSpot(spot);
  await sleep(300);
  const spotsBefore = es.getState().draft.spots?.length ?? 0;
  press('Backspace');
  await sleep(200);
  const spotsAfterBackspace = es.getState().draft.spots?.length ?? 0;
  const heldWhileHealing = ui().showOriginal;
  press('Backspace', 'keyup');
  press('Delete');
  await sleep(400);
  const spotsAfterDelete = es.getState().draft.spots?.length ?? 0;
  mw.esSetHealing(false);
  await sleep(300);

  // Pass 1 — PIXELS, uncropped so both frames show the same field of view and
  // a luma comparison means something: a hard tone edit, then hold and read
  // what the layer actually painted. expEV is set well clear of the fixture's
  // seeded base_exp_ev (~1.9 — untouched photos are not neutral), so the
  // developed frame comes out unmistakably brighter than the original.
  mw.esUpdate({ expEV: 3.5, saturation: -0.9, contrast: 0.5, cropX: 0, cropY: 0, cropW: 1, cropH: 1 });
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);
  await sleep(1500); // the committed rendition lands under the new hash
  point('pointerdown');
  await until(() => layer()?.naturalWidth > 0, 20000);
  const pxOriginal = await lumaOf(layer().src);
  const pxDeveloped = await lumaOf(es.getState().preview?.blob ?? layer().src);
  point('pointerup');

  // Keyboard: Backspace down holds, Backspace up releases.
  press('Backspace');
  await sleep(500);
  const onAfterKeyDown = ui().showOriginal;
  press('Backspace', 'keyup');
  const offAfterKeyUp = ui().showOriginal;
  // A release that lands in another window never reaches us — blur clears it.
  press('Backspace');
  window.dispatchEvent(new Event('blur'));
  const offAfterBlur = ui().showOriginal;

  // Pass 2 — GEOMETRY. A wide crop of a portrait frame: the original's own
  // aspect is nothing like the developed box's, so the containment (rather
  // than a stretch into the cropped box) is unmistakable in the capture.
  mw.esUpdate({ cropX: 0.05, cropY: 0.3, cropW: 0.9, cropH: 0.34 });
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);
  await sleep(1500);
  point('pointerdown');
  const onAfterPointerDown = ui().showOriginal;
  point('pointerup');
  const offAfterPointerUp = ui().showOriginal;
  point('pointerdown'); // left held for the capture
  await until(() => layer()?.naturalWidth > 0, 20000);

  const img = layer();
  const draft = es.getState().draft;
  const box = document.querySelector('[data-testid="original-layer"]').parentElement;
  const boxRect = box.getBoundingClientRect();
  const imgRect = img.getBoundingClientRect();
  const originalUrl = img.src;
  window.__originalProbe = {
    hasButton: !!btn,
    // Backspace holds instead of deleting the spot; Delete still deletes.
    spotsBefore,
    spotsAfterBackspace,
    heldWhileHealing,
    spotsAfterDelete,
    spotSurvivedBackspace: spotsAfterBackspace === spotsBefore,
    deleteRemovedSpot: spotsAfterDelete === spotsBefore - 1,
    onAfterKeyDown,
    offAfterKeyUp,
    offAfterBlur,
    onAfterPointerDown,
    offAfterPointerUp,
    // The layer asks for the base state — no e= is the base hash.
    layerSrcIsBase: /\/img\/\d+\/\d+\?/.test(originalUrl) && !/[?&]e=/.test(originalUrl),
    // Same framing, a hard tone+colour edit apart: the layer really is
    // different pixels, not the developed frame under a different URL.
    baseExpEV: es.getState().baseExpEV,
    lumaOriginal: meanLuma(pxOriginal),
    lumaDeveloped: meanLuma(pxDeveloped),
    meanDiff: meanDiff(pxDeveloped, pxOriginal),
    pixelsDiffer: meanDiff(pxDeveloped, pxOriginal) > 10,
    // Cropped to 0.9×0.34 of the frame: the original keeps the full frame's
    // aspect, so it letterboxes inside the developed box instead of stretching.
    cropped: draft.cropW > 0 && draft.cropW < 1,
    boxAspect: Math.round((boxRect.width / boxRect.height) * 100) / 100,
    // object-contain: the painted image's own aspect, not the box's.
    naturalAspect: Math.round((img.naturalWidth / img.naturalHeight) * 100) / 100,
    fitsInBox: imgRect.width <= boxRect.width + 1 && imgRect.height <= boxRect.height + 1,
  };
  // Released variant: let go only after the measurements above (the layer's
  // nodes are gone the moment React re-renders).
  if (shot === 'original-released') point('pointerup');
  // The capture fires ~4s after this branch returns; re-measure just before it
  // so the probe describes the geometry actually in the PNG, not an earlier one.
  setTimeout(() => {
    const b = document.querySelector('[data-testid="original-layer"]')?.parentElement;
    const i = layer();
    const d = es.getState().draft;
    window.__originalProbe.atCapture = {
      held: ui().showOriginal,
      zoom: ui().loupeZoom,
      crop: d ? [d.cropX, d.cropY, d.cropW, d.cropH] : null,
      boxRect: b ? [Math.round(b.getBoundingClientRect().width), Math.round(b.getBoundingClientRect().height)] : null,
      imgRect: i ? [Math.round(i.getBoundingClientRect().width), Math.round(i.getBoundingClientRect().height)] : null,
      natural: i ? [i.naturalWidth, i.naturalHeight] : null,
      src: i ? i.src.replace(/[?&]t=[^&]*/, '') : null,
    };
  }, 3800);
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
  // slider, +/- steps the focused slider, Tab cycles develop→curve (the tab
  // order is develop, curve, masks, presets, info).
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
} else if (shot === 'maskorder') {
  // Mask reordering: masks composite in list order, so the stack has to be
  // rearrangeable. Driven through the DOM (grip dragstart → row drop) rather
  // than by calling esMoveMask, because the wiring between them is the part
  // that can break.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200); // initial preview settles
  // Idempotence: a previous run's masks persisted to the fixture photo (the
  // `masks` surface precedent).
  mw.esUpdate({ masks: [] });
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);
  ui().setDevelopTab('masks');
  mw.esAddMask('radial'); // index 0
  await sleep(200);
  mw.esUpdateMask(0, { adjust: { expEV: 1.5 } });
  mw.esCommit();
  await sleep(200);
  // A one-mask stack has nothing to reorder, so that row carries no grip.
  const gripCountOne = document.querySelectorAll('[data-testid="mask-grip"]').length;
  mw.esAddMask('linear'); // index 1
  await sleep(200);
  mw.esUpdateMask(1, { adjust: { expEV: -1 } });
  mw.esCommit();
  // Select the radial (index 0) — the selection names a mask by position, so
  // it has to follow the mask across the move, not stay on slot 0.
  mw.esSetActiveMask(0);
  await until(() => mw.esPreviewSettled(), 30000);
  await sleep(300);

  const types = () => (es.getState().draft.masks ?? []).map((m) => m.type);
  const label = () => {
    const h = es.getState().history[es.getState().photoId];
    return h ? h.stack[h.index].label : null;
  };
  const rows = () => [...document.querySelectorAll('[data-testid="mask-row"]')];
  const grips = () => [...document.querySelectorAll('[data-testid="mask-grip"]')];
  const dragRowOnto = (from, to) => {
    const dt = new DataTransfer();
    const opts = { bubbles: true, cancelable: true, dataTransfer: dt };
    grips()[from].dispatchEvent(new DragEvent('dragstart', opts));
    rows()[to].dispatchEvent(new DragEvent('dragover', opts));
    rows()[to].dispatchEvent(new DragEvent('drop', opts));
    grips()[from].dispatchEvent(new DragEvent('dragend', opts));
  };

  const orderBefore = types();
  const gripCount = grips().length;
  dragRowOnto(0, 1); // radial moves under the linear
  await sleep(400);
  const orderAfter = types();
  const movedLabel = label();
  const selectionFollowed =
    es.getState().activeMask === 1 && es.getState().draft.masks[1].type === 'radial';
  // The move is a pixel change, so it commits and re-renders like a slider.
  await until(() => mw.esPreviewSettled(), 30000);

  // Ctrl+Z is the whole point of committing through history rather than
  // patching the draft: one undo puts the stack back.
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'z', ctrlKey: true, bubbles: true }));
  await sleep(600);
  const orderUndone = types();
  await until(() => mw.esPreviewSettled(), 30000);

  // Dropping onto the last row is the only way to reach the bottom slot, so
  // it must not be a no-op. Restores the flipped order for the screenshot.
  dragRowOnto(0, 1);
  await sleep(400);
  mw.esSetActiveMask(1);
  await until(() => mw.esPreviewSettled(), 30000);

  window.__maskOrderProbe = {
    gripCountOne,
    gripCount,
    orderBefore,
    orderAfter,
    reordered: orderBefore.join() === 'radial,linear' && orderAfter.join() === 'linear,radial',
    selectionFollowed,
    movedLabel,
    orderUndone,
    undone: orderUndone.join() === 'radial,linear',
    adjustKept: (es.getState().draft.masks[1].adjust ?? {}).expEV === 1.5,
  };
} else if (shot === 'maskfx') {
  // Mask FX: the one-click Background button — a background-kind AI mask
  // pre-loaded with glow, light streaks and a prism fringe. The probe is the
  // visual proof of the whole feature: the corner (background) must change and
  // the centre (subject) must not.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('masks');
  await sleep(600);
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
    // Local variation around a point: how much detail is left there. A
    // defocus flattens it; the sharp subject keeps it.
    const detail = (fx, fy, r = 3) => {
      const cx = Math.floor(fx * (bmp.width - 1));
      const cy = Math.floor(fy * (bmp.height - 1));
      let sum = 0;
      let n = 0;
      for (let y = Math.max(1, cy - r); y <= Math.min(bmp.height - 1, cy + r); y++) {
        for (let x = Math.max(1, cx - r); x <= Math.min(bmp.width - 1, cx + r); x++) {
          const i = (y * bmp.width + x) * 4;
          sum += Math.abs(d[i] - d[i - 4]);
          n++;
        }
      }
      return n ? sum / n : 0;
    };
    return {
      centre: px(0.5, 0.5),
      corner: px(0.06, 0.06),
      centreDetail: detail(0.5, 0.5),
      cornerDetail: detail(0.06, 0.06),
    };
  };
  // Idempotence: a previous run's masks persisted to the fixture photo. The
  // commit also lands the sharp settle, so before/after is settle-to-settle.
  mw.esUpdate({ masks: [] });
  mw.esCommit();
  await until(() => es.getState().preview?.blob && mw.esPreviewSettled(), 30000);
  const before = await pixelsAt(es.getState().preview.blob);

  document.querySelector('[data-testid="ai-mask-background"]')?.click();
  // Generation runs a local model (seconds warm) and adds the mask on success.
  await until(
    () => (es.getState().draft?.masks ?? []).some(
      (m) => m.type === 'ai' && m.aiKind === 'background' && m.adjust?.streaks,
    ),
    120000,
  );
  await until(() => mw.esPreviewSettled(), 60000);
  const after = await pixelsAt(es.getState().preview.blob);

  // Select the mask and open the Effects sub-block so the screenshot shows
  // the new sliders.
  mw.esSetActiveMask((es.getState().draft.masks?.length ?? 1) - 1);
  await sleep(300);
  const fxToggle = document.querySelector('[data-testid="mask-fx-toggle"]');
  if (fxToggle?.getAttribute('aria-expanded') === 'false') fxToggle.click();
  await sleep(300);

  const mask = es.getState().draft.masks.at(-1);
  window.__maskProbe = {
    fx: true,
    aiKind: mask.aiKind,
    glow: mask.adjust?.glow ?? 0,
    streaks: mask.adjust?.streaks ?? 0,
    cornerDetailBefore: Math.round(before.cornerDetail * 10) / 10,
    cornerDetailAfter: Math.round(after.cornerDetail * 10) / 10,
    centreDetailBefore: Math.round(before.centreDetail * 10) / 10,
    centreDetailAfter: Math.round(after.centreDetail * 10) / 10,
    // The background lost detail…
    cornerSoftened: after.cornerDetail < before.cornerDetail * 0.8,
    // …and the subject kept its pixels.
    centreUnchanged: before.centre.every((v, i) => Math.abs(v - after.centre[i]) <= 4),
    fxRowsShown: document.querySelectorAll('[data-testid="mask-fx-toggle"]').length,
  };
} else if (shot === 'maskremove') {
  // Mask removal: the Remove pill is offered only on mask types whose region
  // is binary and bounded, and toggling it inpaints that region away. Uses a
  // painted brush mask so the check works on any fixture (person detection
  // finds nothing in a landscape).
  ui().setMode('develop');
  ui().setDevelopTab('masks');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200);
  // Sampled at 256px, not 64: the region may be inpainted with more of the
  // same wall/sky, and a coarse downsample would average that difference away.
  const pixelsAt = async (blob) => {
    const bmp = await createImageBitmap(blob, { resizeWidth: 256 });
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
    // A grid across the painted stroke, and one well outside it.
    const region = [];
    for (let fx = 0.42; fx <= 0.58; fx += 0.02) {
      for (let fy = 0.46; fy <= 0.54; fy += 0.02) region.push(px(fx, fy));
    }
    return { region, corner: px(0.06, 0.06) };
  };
  // Idempotence: drop any masks a previous run persisted, and settle so the
  // before/after comparison is settle-to-settle.
  mw.esUpdate({ masks: [] });
  mw.esCommit();
  await until(() => es.getState().preview?.blob && mw.esPreviewSettled(), 30000);
  const before = await pixelsAt(es.getState().preview.blob);

  // A radial mask must NOT offer the pill (its region is soft and unbounded).
  mw.esAddMask('radial');
  await sleep(200);
  mw.esSetActiveMask(0);
  await sleep(300);
  const pillOnRadial = document.querySelectorAll('[data-testid="mask-remove-toggle"]').length;

  // A painted brush mask must.
  mw.esUpdate({ masks: [] });
  mw.esAddMask('brush');
  await sleep(200);
  mw.esUpdateMask(0, {
    strokes: [{ radius: 0.06, feather: 0.4, pts: [0.4, 0.46, 0.5, 0.52, 0.6, 0.5] }],
  });
  mw.esCommit();
  await sleep(300);
  mw.esSetActiveMask(0);
  await sleep(300);
  const pill = document.querySelector('[data-testid="mask-remove-toggle"]');
  pill?.click();
  // Generation downloads nothing here (the model is already on disk from the
  // fill work) but still runs a forward pass.
  await until(() => es.getState().draft?.masks?.[0]?.remove === true, 10000);
  await until(() => mw.esPreviewSettled() && !es.getState().maskFillBusy.length, 120000);
  await sleep(600);
  const after = await pixelsAt(es.getState().preview.blob);

  // Largest per-channel change anywhere across the painted region. The
  // synthesized pixels can legitimately resemble what they replaced (a wall
  // inpainted with more wall), so this is reported as a magnitude rather than
  // asserted against a threshold — internal/pyramid's tests and
  // scripts/maskremove-verify.mjs pin the composite exactly.
  const maxDelta = (a, b) =>
    Math.max(...a.map((p, i) => Math.max(...p.map((v, c) => Math.abs(v - b[i][c])))));
  window.__maskRemoveProbe = {
    pillOnRadial,
    pillOnBrush: pill != null ? 1 : 0,
    pillLit: pill?.getAttribute('aria-pressed') === 'true',
    removeFlag: es.getState().draft?.masks?.[0]?.remove === true,
    // The painted region got new pixels…
    regionDelta: maxDelta(before.region, after.region),
    regionChanged: maxDelta(before.region, after.region) > 0,
    // …and the rest of the frame did not.
    cornerUnchanged: before.corner.every((v, i) => Math.abs(v - after.corner[i]) <= 3),
  };
} else if (shot === 'aitint') {
  // AI mask hover tint: generate an AI mask via the real button (subject by
  // default; ?shotAI=depth|scene picks another kind), hover its row header,
  // and assert the server-rendered red tint appears over the loupe (the only
  // visualization an AI mask has).
  const aiKind = params.get('shotAI') || 'subject';
  const aiRow = { subject: 'Subject ', depth: 'Depth ', scene: 'Scene ' }[aiKind] ?? 'Subject ';
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('masks');
  await sleep(600);
  mw.esUpdate({ masks: [] }); // idempotence: drop persisted masks first
  mw.esCommit();
  await sleep(800);
  document.querySelector(`[data-testid="ai-mask-${aiKind}"]`)?.click();
  // Generation runs a local model (seconds warm; the map may also already
  // exist from a previous run) and adds the mask on success.
  await until(() => (es.getState().draft?.masks ?? []).some((m) => m.type === 'ai'), 120000);
  await sleep(500);
  // Hover the mask row header (React onMouseEnter listens to mouseover).
  const row = [...document.querySelectorAll('span')].find((s) => s.textContent.startsWith(aiRow));
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
  await until(() => document.querySelector('[data-testid="scene-chips"] button'), 120000);
  await sleep(300);
  const chips = [...document.querySelectorAll('[data-testid="scene-chips"] button')];
  const cats = es.getState().aiDetect.class?.categories ?? [];
  // Chip hover tints the region through the shared AI pick overlay — no arming
  // needed, the overlay stays mounted through Develop.
  let tintShown = false;
  if (cats.length) {
    mw.esSetAIHover({ kind: 'class', id: cats[0].id });
    tintShown = await until(() => {
      const img = document.querySelector('[data-testid="ai-pick-tint"] img');
      return img && img.complete && img.naturalWidth > 0;
    }, 30000).then(() => true, () => false);
    await sleep(600); // tint fade-in
    mw.esSetAIHover(null);
  }
  chips[0]?.click();
  await until(() => (es.getState().draft?.masks ?? []).some((m) => m.aiKind === 'class'), 5000);
  await sleep(400);
  window.__maskProbe = {
    chips: chips.map((c) => c.textContent),
    classMaskAdded: (es.getState().draft?.masks ?? []).some((m) => m.aiKind === 'class'),
    tintShown,
    // The chips must survive an add (regression: they used to vanish), and the
    // scene pick tool stays armed for more picks.
    chipsAfterAdd: document.querySelectorAll('[data-testid="scene-chips"] button').length,
    armedAfterAdd: es.getState().aiPickArmed,
  };
} else if (shot === 'personpick') {
  // Person pick tool: click the People button (model + map must be present —
  // the wrapper seeds a plane when the fixture has no people), wait for pick
  // mode + chips, hover person 1 through the session so the loupe tint
  // renders deterministically, and capture the tinted loupe.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('masks');
  await sleep(600);
  mw.esUpdate({ masks: [] }); // idempotence: drop persisted masks first
  mw.esCommit();
  await sleep(800);
  document.querySelector('[data-testid="ai-mask-person"]')?.click();
  await until(() => es.getState().aiDetect.person != null, 120000);
  await sleep(300);
  const pick = es.getState().aiDetect.person;
  if (pick?.instances?.length) {
    mw.esSetAIHover({ kind: 'person', id: pick.instances[0].id });
    await until(() => {
      const img = document.querySelector('[data-testid="ai-pick-tint"] img');
      return img && img.complete && img.naturalWidth > 0;
    }, 30000);
    await sleep(600); // tint fade-in
  }
  window.__maskProbe = {
    overlayMounted: !!document.querySelector('[data-testid="ai-pick-overlay"]'),
    armed: es.getState().aiPickArmed,
    instances: pick?.instances?.length ?? 0,
    chips: [...document.querySelectorAll('[data-testid="person-chips"] button')].map((c) => c.textContent),
    tintShown: !!document.querySelector('[data-testid="ai-pick-tint"] img'),
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
} else if (shot === 'lens') {
  // Lens profile correction: open Develop's Detail group, assert the section
  // names the matched profile and offers a slider per correction the profile
  // actually carries, then flip Off and back and assert the render responds.
  // The fixture body is a fixed-lens compact, so this also covers the
  // match-by-mount path.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('develop');
  mw.setEditGroupOpen('detail', true);
  // Idempotence: a previous run may have left the correction switched off.
  mw.esUpdate({ lensMode: undefined, lensDistortion: undefined, lensVignetting: undefined, lensCA: undefined });
  mw.esCommit();
  await sleep(1500); // the profile lookup + a corrected preview
  const sectionOf = () =>
    [...document.querySelectorAll('span')]
      .find((n) => n.textContent?.trim() === 'Lens correction')
      ?.closest('div.border-t');
  await until(() => sectionOf() != null, 20000);
  const section = sectionOf();
  const sliderLabels = () =>
    [...(section?.querySelectorAll('span') ?? [])].map((n) => n.textContent?.trim());
  const enabledSliders = () =>
    [...(section?.querySelectorAll('[data-slot="slider-thumb"]') ?? [])].length;

  const lumaOf = async () => {
    const img = document.querySelector('[data-testid="loupe-image"]') ?? document.querySelector('main img');
    if (!img || !img.complete || !img.naturalWidth) return null;
    const c = document.createElement('canvas');
    c.width = 48;
    c.height = Math.max(1, Math.round((48 * img.naturalHeight) / img.naturalWidth));
    const ctx = c.getContext('2d');
    ctx.drawImage(img, 0, 0, c.width, c.height);
    const d = ctx.getImageData(0, 0, c.width, c.height).data;
    // The extreme corner is where vignetting lives.
    const i = 0;
    return (d[i] * 299 + d[i + 1] * 587 + d[i + 2] * 114) / 1000;
  };
  const cornerOn = await lumaOf();
  mw.esUpdate({ lensMode: 'off' });
  mw.esCommit();
  await sleep(2000);
  const cornerOff = await lumaOf();
  mw.esUpdate({ lensMode: undefined });
  mw.esCommit();
  await sleep(2000);

  // Bring the section into view so the screenshot shows what was asserted.
  section?.scrollIntoView({ block: 'center' });
  await sleep(400);

  window.__lensProbe = {
    sectionFound: !!section,
    profileLine: section?.querySelector('p')?.textContent ?? '',
    labels: sliderLabels(),
    thumbs: enabledSliders(),
    cornerOn,
    cornerOff,
    modeAfterReset: es.getState().draft?.lensMode ?? '',
  };
} else if (shot === 'bw') {
  // B&W treatment: flip the Color/B&W row in the Color group and assert the
  // developed preview actually goes neutral, that a mixer band still moves
  // the gray (the colored-filter behaviour that makes this more than a
  // desaturation), and that a split tone tints the result (sepia). Driven
  // through the panel's own control so the wiring is under test, not just
  // the render.
  // Progress is recorded as it goes: the harness forwards only the returned
  // probe, so a bare throw would say "timeout" and nothing about where.
  const P = { stage: 'start' };
  window.__bwProbe = P;
  try {
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('develop');
  mw.setEditGroupOpen('color', true);
  P.stage = 'panel-open';
  // Idempotence: a previous run may have left the photo converted. The mixer
  // bands reset to a zeroed array, not undefined — the panel indexes them.
  mw.esUpdate({ bw: false, hslLum: Array(8).fill(0), splitHighlightAmt: 0, splitShadowAmt: 0 });
  mw.esCommit();
  await sleep(1500);

  const stats = async () => {
    const img = document.querySelector('[data-testid="loupe-image"]') ?? document.querySelector('main img');
    if (!img || !img.complete || !img.naturalWidth) return null;
    const c = document.createElement('canvas');
    c.width = 64;
    c.height = Math.max(1, Math.round((64 * img.naturalHeight) / img.naturalWidth));
    const ctx = c.getContext('2d');
    ctx.drawImage(img, 0, 0, c.width, c.height);
    const d = ctx.getImageData(0, 0, c.width, c.height).data;
    let chroma = 0, luma = 0, r = 0, g = 0, b = 0, n = 0;
    for (let i = 0; i + 3 < d.length; i += 4) {
      chroma += Math.max(d[i], d[i + 1], d[i + 2]) - Math.min(d[i], d[i + 1], d[i + 2]);
      luma += (d[i] * 299 + d[i + 1] * 587 + d[i + 2] * 114) / 1000;
      r += d[i]; g += d[i + 1]; b += d[i + 2];
      n++;
    }
    return { chroma: chroma / n, luma: luma / n, r: r / n, g: g / n, b: b / n };
  };
  const colorStats = await stats();
  P.stage = 'color-measured';

  // The treatment row itself: click the B&W segment.
  const bwButton = () =>
    [...document.querySelectorAll('button')].find((n) => n.textContent?.trim() === 'B&W');
  await until(() => bwButton() != null, 20000);
  P.stage = 'button-found';
  bwButton().click();
  await sleep(2500);
  const bwStats = await stats();
  const bwDraft = es.getState().draft?.bw ?? false;
  P.stage = 'converted';

  // Saturation must read as inert while the split-tone rows stay live.
  const rowOf = (label) =>
    [...document.querySelectorAll('span')]
      .find((n) => n.textContent?.trim() === label)
      ?.closest('div');
  const disabledRow = (label) =>
    rowOf(label)?.querySelector('[data-slot="slider"][data-disabled], [data-disabled] [data-slot="slider"]') != null ||
    rowOf(label)?.className.includes('opacity-50');
  const satInert = disabledRow('Saturation');
  const tintLive = !disabledRow('Highlight tint amount');
  const mixerLabel = [...document.querySelectorAll('span')].some((n) => n.textContent?.trim() === 'B&W mix');

  // A band still steers the gray: drop the red band and the frame must move.
  const band = Array(8).fill(0);
  band[0] = -1;
  mw.esUpdate({ hslLum: band });
  mw.esCommit();
  await sleep(2500);
  const filtered = await stats();

  // Sepia: warm both ends of the split tone over the conversion.
  mw.esUpdate({
    hslLum: Array(8).fill(0),
    splitShadowHue: 40, splitShadowAmt: 0.8,
    splitHighlightHue: 40, splitHighlightAmt: 0.8,
  });
  mw.esCommit();
  await sleep(2500);
  const sepia = await stats();

  Object.assign(P, {
    stage: 'done',
    colorChroma: colorStats?.chroma,
    bwChroma: bwStats?.chroma,
    bwDraft,
    satInert,
    tintLive,
    mixerLabel,
    bwLuma: bwStats?.luma,
    filteredLuma: filtered?.luma,
    sepiaWarm: sepia ? sepia.r > sepia.g && sepia.g > sepia.b : false,
  });
  } catch (err) {
    P.error = String(err);
  }
} else if (shot === 'tonecurve') {
  // Point tone curve: from a curve-free state, commit a midtone-lift curve
  // and assert (a) the developed preview brightens the mids while the
  // endpoints hold, and (b) the widget renders one control point per curve
  // point. Then Reset must fold the curve back to neutral (undefined).
  // The editor lives on its own Curve tab (right of Develop).
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('curve');
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
  const luma = (p) => (p[0] * 299 + p[1] * 587 + p[2] * 114) / 1000;
  // Idempotence: a previous run's curves persisted to the fixture photo.
  mw.esUpdate({
    toneCurve: undefined,
    toneCurveR: undefined,
    toneCurveG: undefined,
    toneCurveB: undefined,
  });
  mw.esCommit();
  await until(() => es.getState().preview?.blob && mw.esPreviewSettled(), 30000);
  const before = await pixelsAt(es.getState().preview.blob);
  const lift = [
    { x: 0, y: 0 },
    { x: 0.5, y: 0.75 },
    { x: 1, y: 1 },
  ];
  mw.esUpdate({ toneCurve: lift });
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);
  const after = await pixelsAt(es.getState().preview.blob);
  const svg = document.querySelector('svg[aria-label="Tone curve"]');
  const pointCount = svg?.querySelectorAll('circle').length ?? 0;
  const polyline = svg?.querySelector('polyline')?.getAttribute('points') ?? '';

  // Per-channel: a red-lift curve on top of the master must push the center
  // pixel warm (r up relative to b) — the master curve alone can't do that.
  const chanBtn = (label) =>
    [...(svg?.parentElement?.querySelectorAll('button') ?? [])].find(
      (b) => b.textContent.trim().replace(/•$/, '') === label,
    );
  chanBtn('R')?.click();
  await sleep(150);
  const chanAfterTab = svg?.dataset.channel;
  mw.esUpdate({
    toneCurveR: [
      { x: 0, y: 0 },
      { x: 0.5, y: 0.8 },
      { x: 1, y: 1 },
    ],
  });
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);
  const red = await pixelsAt(es.getState().preview.blob);
  // Guide lines: the master curve stays drawn faintly while R is selected.
  const polylineCount = svg?.querySelectorAll('polyline').length ?? 0;

  // Reset clears only the SELECTED channel (R), leaving the master curve.
  const resetBtn = () =>
    [...(svg?.parentElement?.querySelectorAll('button') ?? [])].find(
      (b) => b.textContent.trim() === 'Reset',
    );
  resetBtn()?.click();
  await sleep(250);
  const afterResetR = {
    r: es.getState().draft.toneCurveR == null,
    masterKept: es.getState().draft.toneCurve != null,
  };
  // Back to the master tab and reset it too — both channels now neutral.
  chanBtn('RGB')?.click();
  await sleep(150);
  resetBtn()?.click();
  await sleep(250);
  const resetToNeutral =
    es.getState().draft.toneCurve == null && es.getState().draft.toneCurveR == null;

  // Pointer path. Everything above drives state directly and never touches the
  // widget's own drag handling, which is where a real curve edit comes from.
  // Drive it with synthetic pointers instead. The press MUST come back
  // defaultPrevented: the widget sits between the "Curve" heading and the help
  // paragraph, so an un-prevented press is read as a text selection of the
  // panel — the cursor turns into a drag ghost and the drag is lost.
  const at = (fx, fy) => {
    const r = svg.getBoundingClientRect();
    return { clientX: r.left + r.width * fx, clientY: r.top + r.height * fy };
  };
  const ptr = (target, type, fx, fy, buttons = 1) => {
    const e = new PointerEvent(type, {
      bubbles: true,
      cancelable: true,
      pointerId: 7,
      isPrimary: true,
      button: type === 'pointermove' ? -1 : 0,
      buttons,
      ...at(fx, fy),
    });
    target.dispatchEvent(e);
    return e;
  };
  const midY = () => es.getState().draft.toneCurve?.[1]?.y;
  // From neutral, a press on empty grid adds a point and drags it in the same
  // gesture — the widget's primary interaction.
  const pressPrevented = ptr(svg, 'pointerdown', 0.5, 0.5).defaultPrevented;
  await sleep(80);
  ptr(svg, 'pointermove', 0.5, 0.25);
  await sleep(80);
  const draggedTo = midY();
  ptr(svg, 'pointerup', 0.5, 0.25, 0);
  await sleep(200);
  const committedTo = midY();
  // Grab the point itself and drag it back down.
  const dot = svg.querySelectorAll('circle')[1];
  ptr(dot, 'pointerdown', 0.5, 0.25);
  await sleep(80);
  ptr(svg, 'pointermove', 0.5, 0.6);
  await sleep(80);
  const regrabbedTo = midY();
  ptr(svg, 'pointerup', 0.5, 0.6, 0);
  await sleep(200);
  // A press that lands on the grid at an existing point's x — level with it but
  // nowhere near it in y — grabs that point instead of doing nothing (it can't
  // add one there: a second point on the same x is an ambiguous drag).
  ptr(svg, 'pointerdown', 0.5, 0.95);
  await sleep(80);
  const pointsAfterDrags = svg.querySelectorAll('circle').length;
  const beforeBare = midY();
  // ...and a press whose release we never see (capture stolen, pointer lifted
  // off-window) must not leave that point stuck to the bare cursor.
  ptr(svg, 'pointermove', 0.5, 0.05, 0); // no button held
  await sleep(80);
  ptr(svg, 'pointermove', 0.5, 0.95, 0);
  await sleep(150);
  const afterBareMoves = midY();

  // Re-apply a master + red grade purely so the capture shows a real curve
  // (the idempotence step at the top clears all four, so repeat runs are
  // unaffected by what's left on screen here).
  mw.esUpdate({
    toneCurve: [
      { x: 0, y: 0 },
      { x: 0.25, y: 0.18 },
      { x: 0.75, y: 0.85 },
      { x: 1, y: 1 },
    ],
    toneCurveR: [
      { x: 0, y: 0 },
      { x: 0.5, y: 0.62 },
      { x: 1, y: 1 },
    ],
  });
  mw.esCommit();
  await until(() => mw.esPreviewSettled(), 30000);

  window.__maskProbe = {
    // The editor lives on the Curve tab, not inline in Develop.
    onCurveTab: ui().developTab === 'curve',
    curveTabLabels: [...document.querySelectorAll('[aria-label="Panel"] [role="radio"]')].map(
      (b) => b.textContent.trim(),
    ),
    widgetPresent: !!svg,
    pointCount,
    polylineDrawn: polyline.split(' ').length >= 10,
    centerLumaBefore: Math.round(luma(before.center)),
    centerLumaAfter: Math.round(luma(after.center)),
    // A midtone-lift curve must brighten mids; the corner (near black) barely
    // moves since the curve pins (0,0).
    centerBrightened: luma(after.center) > luma(before.center) + 8,
    // Per-channel probes.
    chanAfterTab,
    // R−B of the center pixel: master-only vs. master+red-lift.
    redRB: [after.center[0] - after.center[2], red.center[0] - red.center[2]],
    redWarmed: red.center[0] - red.center[2] > after.center[0] - after.center[2] + 4,
    // master + R drawn (the selected line plus one guide)
    polylineCount,
    afterResetR,
    resetToNeutral,
    // Drag handling, driven through the widget's own pointer path.
    pressPrevented,
    // Press at mid-height, drag to 3/4 up: the new point tracks the pointer
    // and survives the release (0.75 in curve space, y counted upwards).
    dragTracks: draggedTo != null && Math.abs(draggedTo - 0.75) < 0.03,
    dragCommitted: committedTo != null && Math.abs(committedTo - 0.75) < 0.03,
    // Pressing the point itself grabs it rather than stacking a new one.
    regrabTracks: regrabbedTo != null && Math.abs(regrabbedTo - 0.4) < 0.03,
    // Still the two endpoints plus the one point added at the top: neither
    // grab stacked another one on its x.
    pointsAfterDrags,
    // A drag whose release went missing ends instead of following the cursor.
    bareMoveIgnored: afterBareMoves === beforeBare,
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
} else if (shot === 'naturalgrid') {
  // Natural thumbFit must size each grid cell to the RENDERED aspect —
  // cropaspect-verify.mjs has committed a half-width crop on photo #2 and a
  // quarter turn on photo #3, so their cells must read ~0.75 and ~0.67
  // against the uncropped ~1.5. Probes the live DOM boxes, not the layout
  // math that produced them.
  ui().setMode('library');
  mw.useUIStore.setState({ thumbFit: 'natural' });
  await sleep(1200);
  const cellAspect = (name) => {
    const el = document.querySelector(`[title="${name}"]`);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return r.height > 0 ? Math.round((r.width / r.height) * 100) / 100 : null;
  };
  window.__naturalGridProbe = {
    fullAspect: cellAspect('sample1.arw'),
    croppedAspect: cellAspect('sample2.arw'),
    rotatedAspect: cellAspect('sample3.arw'),
  };
} else if (shot === 'share' || shot === 'share-publish') {
  // The share dialog, reached the way a user reaches it: right-click a shoot
  // in the rail. Needs MARRAW_SHOT_OWN_DAEMON=1 — the shared `marrawd --dev`
  // registers no share listeners at all (see main.go), so Share.Status reports
  // nowhere to serve from and the dialog refuses to mint against it.
  //
  // `share-publish` goes one click further, to the exposure warning that arms
  // when creating the link would raise a funnel.
  ui().setMode('library');
  // "Share album…" is on a SHOOT row, not a library-root row, so the folder's
  // parent goes in as a parent root and the shoot appears beneath it.
  const folder = ui().folderPath ?? new URLSearchParams(location.search).get('folder');
  const parent = folder.replace(/[\\/][^\\/]+$/, '');
  await mw.setLibraryRoots([
    { path: parent, alias: '', includeSubfolders: false, photoCount: 0, isParent: true },
  ]);
  await sleep(1500);
  window.__shareProbe = { stage: 'waiting for rail' };
  try {
    if (!document.querySelector('[data-testid="rail-shoot"]')) {
      document.querySelector('[data-testid="rail-parent"]')?.click();
      await sleep(1200);
    }
    const shoot = await until(() => document.querySelector('[data-testid="rail-shoot"]'), 10000);
  window.__shareProbe.stage = 'opening context menu';
  const box = shoot.getBoundingClientRect();
  shoot.dispatchEvent(
    new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
      button: 2,
      buttons: 2,
      clientX: box.left + 20,
      clientY: box.top + 10,
    }),
  );
  const item = await until(
    () =>
      [...document.querySelectorAll('[role="menuitem"]')].find((e) =>
        e.textContent.includes('Share album'),
      ),
    8000,
  );
  window.__shareProbe.stage = 'opening dialog';
  item.click();
  const dialog = await until(() => {
    const d = [...document.querySelectorAll('[role="dialog"]')].find((e) =>
      e.textContent.includes('Who can open it'),
    );
    // Wait for Share.Status to land, or the reach control reads as if this
    // machine had no tailnet and the shot catches the wrong branch.
    return d && d.textContent.includes('Tailscale') ? d : null;
  }, 12000);
  if (shot === 'share-publish') {
    [...dialog.querySelectorAll('button')]
      .find((b) => b.textContent.trim() === 'Create link')
      ?.click();
    await sleep(400);
  }
  const text = dialog.textContent ?? '';
  window.__shareProbe = {
    stage: 'done',
    offersBothReaches:
      text.includes('Anyone with the link') && text.includes('Only my devices'),
    // The warning must appear on the second surface and only there.
    warnsAboutPublicInternet: text.includes('publishes your computer on the public internet'),
    saysNoGuarantees: text.includes('no guarantees'),
    // Case-insensitive: consenting turns "Create link" into "Publish and
    // create link", and that relabel is the point of the second surface.
    buttonLabel:
      [...dialog.querySelectorAll('button')]
        .map((b) => b.textContent.trim())
        .find((t) => /create link/i.test(t)) ?? '',
    // Must stay false: the shot is for review, and a real token would be in it.
    leaksToken: /\b[0-9a-f]{32}\b/.test(text),
  };
  } catch (err) {
    // Report where it stopped instead of failing the whole run: the capture
    // still happens, and the frame shows what the driver was looking at.
    window.__shareProbe.error = String(err);
  }
} else if (shot === 'addfolder') {
  ui().setAddFolderOpen(true);
} else if (shot === 'shortcuts') {
  ui().setShortcutsOpen(true);
} else if (shot === 'light') {
  document.documentElement.classList.remove('dark');
} else if (shot === 'palette') {
  // Over Develop, like the README's jump shot — not over the startup grid.
  ui().setMode('develop');
  await sleep(1500);
  ui().setPaletteOpen(true);
} else if (shot === 'export') {
  ui().setExportOpen(true);
} else if (shot === 'export-copy') {
  // Copy-to-clipboard button: with a single photo selected the button shows;
  // clicking it runs the whole path (render RPC → blob → Electron IPC →
  // clipboard.writeImage) and lands the success toast. With a multi-photo
  // selection the button must be absent.
  ui().setExportOpen(true);
  await sleep(400);
  const copyBtn = () =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === 'Copy to clipboard');
  const singleShown = !!copyBtn();
  copyBtn()?.click();
  await until(() => document.body.textContent.includes('Copied — ready to paste'), 120000);
  const dialogClosed = !ui().exportOpen;
  ui().selectAll(ui().visibleIds);
  ui().setExportOpen(true);
  await sleep(400);
  const multiShown = !!copyBtn();
  // Back to a single selection so the capture shows the button.
  ui().setExportOpen(false);
  await sleep(200);
  ui().focus(ui().visibleIds[0]);
  ui().setExportOpen(true);
  await sleep(300);
  window.__exportCopyProbe = { singleShown, dialogClosed, multiShown };
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
} else if (shot === 'export-presets') {
  // Export presets: save the current dialog settings as a named preset via
  // the real menus and naming input, diverge a setting to get the
  // "(modified)" suffix, Update to clear it, and leave the preset applied
  // for the capture. Idempotent: leftover UITEST presets are cleared first.
  const setInput = (el, v) => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(el, v);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const btn = (label) =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === label);
  const item = (label) =>
    [...document.querySelectorAll('[role="menuitem"]')].find((el) =>
      el.textContent.trim().startsWith(label),
    );
  mw.setExportPresets(ui().exportPresets.filter((p) => !p.name.startsWith('UITEST')));
  await sleep(300);
  ui().setExportOpen(true);
  await sleep(400);
  // A known start state so the TIFF click below is a real divergence.
  btn('JPEG')?.click();
  await sleep(200);
  btn('Save…')?.click();
  await until(() => item('Save as new preset…'));
  item('Save as new preset…').click();
  const nameInput = await until(() => document.querySelector('input[aria-label="Preset name"]'));
  setInput(nameInput, 'UITEST preset');
  await sleep(150);
  btn('Save')?.click();
  await sleep(500);
  const savedInStore = ui().exportPresets.some((p) => p.name === 'UITEST preset');
  const picker = () =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.includes('UITEST preset'));
  const activeShown = !!picker() && !picker().textContent.includes('(modified)');
  // Diverge: switch the format — the picker must flag the divergence.
  btn('TIFF')?.click();
  await sleep(300);
  const modifiedShown = !!picker() && picker().textContent.includes('(modified)');
  // Update re-snapshots into the preset, keeping id and name; suffix clears.
  btn('Save…')?.click();
  await until(() => item('Update'));
  item('Update').click();
  await sleep(500);
  const updateClears = !!picker() && !picker().textContent.includes('(modified)');
  const updateStored =
    ui().exportPresets.find((p) => p.name === 'UITEST preset')?.options.format === 'tiff8';
  window.__exportPresetsProbe = {
    savedInStore,
    activeShown,
    modifiedShown,
    updateClears,
    updateStored,
  };
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
} else if (
  shot === 'remote' ||
  shot === 'remote-top' ||
  shot === 'remote-add' ||
  shot === 'remote-add-found' ||
  shot === 'pairing-selfpair' ||
  shot === 'pairing-approve'
) {
  // Remote pairing. These need a daemon that is actually REACHABLE — the
  // shared `marrawd --dev` on 8483 is loopback-only, so it serves none of the
  // pairing routes. Run them with MARRAW_SHOT_OWN_DAEMON=1 (see shot.mjs),
  // which spawns a real daemon bound per the run's prefs.
  // The `remote-add-found` variant needs MARRAW_UITEST_HOSTS in the shot env
  // (see main.cjs): a scan on one machine can only ever find nothing, since
  // the shell filters out its own addresses.
  ui().setSettingsOpen(true, 'Remote');
  await sleep(600);
  if (shot === 'remote-top') {
    // The README shot. Deliberately NOT scrolled: the rows below the fold are
    // this machine's reachable addresses and its pairing token, neither of
    // which belongs in a published screenshot — `leaksToken` guards that.
    // "Approved computers" does not render while the list is empty, so it is
    // absent here; seeding a real entry would need `pairing-selfpair`, which
    // currently finds nothing (its scan moved into the daemon and the machine
    // self-excludes, so the MARRAW_UITEST_HOSTS stub it documents is gone).
    await sleep(900);
    const text = document.querySelector('[role="dialog"]')?.textContent ?? '';
    window.__remoteProbe = {
      showsToggle: text.includes('Allow remote connections'),
      showsDeviceName: text.includes("This computer's name"),
      showsApproved: text.includes('Approved computers'),
      approvedRows: document.querySelectorAll('[data-testid="approved-devices"] > div').length,
      // Must stay false, or the shot leaks a credential.
      leaksToken: /\b[0-9a-f]{32}\b/.test(text),
    };
  }
  if (shot === 'remote') {
    // The host half sits below the fold; scroll to it so the shot shows the
    // reachability rows, and report what they say.
    const pane = [...document.querySelectorAll('[role="dialog"] *')].find(
      (e) => e.scrollHeight > e.clientHeight + 40 && e.clientHeight > 200,
    );
    if (pane) pane.scrollTop = pane.scrollHeight;
    await sleep(1200);
    const text = document.querySelector('[role="dialog"]')?.textContent ?? '';
    window.__remoteProbe = {
      showsAddresses: text.includes('Reachable at'),
      addressLine: (text.match(/Reachable at.*?((?:\d{1,3}\.){3}\d{1,3}:\d+)/s) ?? [])[1] ?? '',
      warnsWhenInvisible: text.includes('Not visible to searches'),
      showsDeviceName: text.includes("This computer's name"),
    };
  }
  if (shot === 'pairing-selfpair') {
    // The whole add-a-connection flow against our OWN daemon, driven through
    // the real UI: scan (stubbed to point at loopback via MARRAW_UITEST_HOSTS)
    // → Connect → the approval dialog opens in this same window → Allow.
    //
    // The regression this exists for: approving saves the connection, which
    // re-renders the list, which used to hand the waiting panel a fresh
    // callback and restart the pairing — popping a SECOND request on the host
    // moments after the first was approved.
    const trail = [];
    const snap = (label) =>
      trail.push(
        `${label}: wait=${document.querySelector('[data-testid="pairing-wait-code"]')?.textContent ?? '-'} ` +
          `host=${document.querySelector('[data-testid="pairing-code"]')?.textContent ?? '-'} ` +
          `allow=${[...document.querySelectorAll('button')].some((b) => b.textContent === 'Allow')}`,
      );

    [...document.querySelectorAll('button')]
      .find((b) => b.textContent.startsWith('Add connection'))
      ?.click();
    await sleep(2000);
    const connect = [...document.querySelectorAll('button')].find(
      (b) => b.textContent === 'Connect',
    );
    connect?.click();
    for (let i = 0; i < 6; i++) {
      await sleep(1000);
      snap(`t+${i + 1}s`);
    }
    const waitCode = document.querySelector('[data-testid="pairing-wait-code"]')?.textContent ?? '';
    const hostCode = document.querySelector('[data-testid="pairing-code"]')?.textContent ?? '';
    [...document.querySelectorAll('button')]
      .find((b) => b.textContent === 'Allow')
      ?.click();
    // Long enough that a restarted pairing would have asked again by now.
    await sleep(4000);
    snap('after-allow');
    window.__pairingProbe = {
      apiPort: new URLSearchParams(location.search).get('apiPort'),
      connectClicked: !!connect,
      codesMatched: !!waitCode && waitCode === hostCode,
      // The point of the test: nothing may be waiting for approval afterwards.
      dialogAfterApprove: !!document.querySelector('[data-testid="pairing-approval"]'),
      savedConnections: (await window.marraw.listRemotes()).length,
      approvedDevices: document.querySelectorAll('[data-testid="approved-devices"] > div').length,
      trail,
    };
  }
  if (shot === 'remote-add' || shot === 'remote-add-found') {
    [...document.querySelectorAll('button')]
      .find((b) => b.textContent.startsWith('Add connection'))
      ?.click();
    // The scan runs mDNS for 2.5s and probes for up to 1.5s more.
    await sleep(4500);
  } else if (shot === 'pairing-approve') {
    // A real request against our own daemon, driven through the production
    // path: window.marraw.pairRemote runs in the main process. A fetch from
    // here CANNOT work — the daemon sends no CORS headers on /pair/*, which
    // is exactly what stops a web page driving this flow — so going through
    // the bridge is not a workaround, it is the only door there is.
    const port = new URLSearchParams(location.search).get('apiPort');
    const req = await window.marraw.pairRemote(`127.0.0.1:${port}`);
    // The dialog opens off the ListPairingRequests subscription push.
    await sleep(1500);
    window.__pairingProbe = {
      requested: req.ok === true,
      requestError: req.error ?? '',
      dialogOpen: !!document.querySelector('[data-testid="pairing-approval"]'),
      shownName: document.querySelector('[data-testid="pairing-name"]')?.textContent ?? '',
      codesMatch: req.code === document.querySelector('[data-testid="pairing-code"]')?.textContent,
    };
  }
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
} else if (shot === 'updates' || shot === 'updates-downloading' || shot === 'updates-rail') {
  // Settings → Updates and the rail's pending-update row. Only a packaged
  // build can ever see a real update (the shell's updater is inert under
  // UITEST), so the phase is seeded straight into the store — after the
  // store's own initial read has landed, or it would overwrite the seed.
  // Needs APPIMAGE=1 in the shot env: that's what makes the shell report a
  // self-updating packaging on Linux, hence the pane and the rail row.
  await sleep(400);
  const seed = {
    'updates': { status: 'downloaded', version: '0.9.0', percent: 100 },
    'updates-downloading': {
      status: 'downloading',
      version: '0.9.0',
      percent: 43.2,
      transferred: 41_500_000,
      total: 96_000_000,
      bytesPerSecond: 5_400_000,
    },
    'updates-rail': { status: 'available', version: '0.9.0' },
  }[shot];
  mw.useUpdateStore.setState((s) => ({
    state: { ...s.state, ...seed, error: '', checkedAt: Date.now() },
    currentVersion: '0.9.0-beta.4',
    loaded: true,
  }));
  if (shot !== 'updates-rail') {
    ui().setSettingsOpen(true, 'Updates');
    await sleep(400);
    const dlg = document.querySelector('[role="dialog"]');
    window.__updatesProbe = {
      tab: !!dlg && [...dlg.querySelectorAll('button')].some((b) => b.textContent === 'Updates'),
      version: !!dlg && dlg.textContent.includes('0.9.0-beta.4'),
      check: !!dlg && dlg.textContent.includes('Check for updates'),
      action:
        !!dlg &&
        dlg.textContent.includes(shot === 'updates' ? 'Restart & install' : 'Downloading 0.9.0'),
    };
  } else {
    await sleep(300);
    const row = document.querySelector('[data-testid="rail-update"]');
    window.__updatesProbe = { railText: row?.textContent ?? null };
  }
} else if (shot === 'remote') {
  // Settings → Remote: persist enabled first so the section mounts with the
  // Port row and pairing token shown (the dev-attach daemon leaves
  // restartRequired false, so no amber banner). Then open Settings and click
  // the Remote tab; wait for the server-side pairing token to land.
  await window.marraw?.setRemoteAccess?.({ enabled: true, port: 8482 });
  ui().setSettingsOpen(true);
  await sleep(300);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'Remote')?.click();
  await sleep(400);
  const dlg = document.querySelector('[role="dialog"]');
  // The token row renders '…' until System.GetRemoteAccess resolves.
  await until(() => dlg && !dlg.querySelector('.font-mono')?.textContent?.includes('…'), 8000);
  await sleep(200);
  window.__remoteProbe = {
    hasToggle: !!dlg && dlg.textContent.includes('Allow remote connections'),
    hasPort: !!dlg && dlg.textContent.includes('Remote machines connect to this port'),
    tokenLoaded: !!dlg && !dlg.querySelector('.font-mono')?.textContent?.includes('…'),
  };
} else if (shot === 'presets') {
  // The Presets tab with saved looks: seeds two user presets (one full
  // absolute, one partial+relative), applies the first so the Amount
  // scrubber shows, and probes hover preview + the amount lerp.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  const look = { ...es.getState().draft, contrast: 0.25, vibrance: 0.3, splitShadowHue: 200, splitShadowAmt: 0.2, rotate: 0, flipH: false, cropX: 0, cropY: 0, cropW: 0, cropH: 0, cropAngle: 0, masks: undefined, spots: undefined };
  const presets = [
    { id: 'shot-full', name: 'Punchy look', params: look, baseExpEV: es.getState().baseExpEV || undefined },
    { id: 'shot-tone', name: 'Warm tone (partial)', params: { ...look, splitShadowHue: 35 }, sections: ['tone', 'color'], relative: true },
  ];
  mw.setUserPresets(presets);
  ui().setDevelopTab('presets');
  await sleep(2500); // preset card thumbnails render
  // Hover probe: the loupe must paint the override without touching draft.
  const draftBefore = JSON.stringify(es.getState().draft);
  mw.esHoverPreset(presets[0]);
  await sleep(700); // debounce + low-res frame
  const hoverSet = es.getState().hoverParams != null;
  const draftUntouched = JSON.stringify(es.getState().draft) === draftBefore;
  mw.esHoverEnd();
  await sleep(300);
  const hoverCleared = es.getState().hoverParams == null;
  // Apply + scrub to 60%: contrast lands at base + 0.6×(0.25 − base).
  // Hover at tile depth (1:1 zoom): the settled-clear effect must not evict
  // the hover frame — esPreviewSettled is false while a hover overlay shows,
  // even though the draft sits at the history head.
  ui().setLoupeZoom(1);
  await sleep(800);
  mw.esHoverPreset(presets[0]);
  await sleep(900); // debounce + low-res frame + any clear effect
  const tilesHoverSet = es.getState().hoverParams != null;
  const tilesPreviewShown = es.getState().preview != null;
  mw.esHoverEnd();
  ui().setLoupeZoom('fit');
  await sleep(500);
  mw.esApplyUserPreset(presets[0]);
  await until(() => es.getState().lastPresetApply != null);
  const applied = es.getState().draft.contrast;
  mw.esSetPresetAmount(0.6);
  await sleep(200);
  const scrubbed = es.getState().draft.contrast;
  mw.esCommitPresetAmount();
  window.__presetsProbe = {
    hoverSet,
    draftUntouched,
    hoverCleared,
    tilesHoverSet,
    tilesPreviewShown,
    applied,
    scrubbed,
    amountShown: !!es.getState().lastPresetApply,
  };
} else if (shot === 'presetmasks') {
  // A preset carrying ONLY smart masks: every look-group chip off, the Smart
  // masks chip on. Saving used to be blocked outright (the section guard
  // required at least one group), so this drives the real UI — chips, name
  // field, Save button — and then checks the saved shape and what applying
  // it does: the recipes must land and the look must not move a hair.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200); // initial preview settles
  const setInput = (el, v) => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(el, v);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const btn = (label) =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === label);
  const chipOn = (b) => !!b && b.className.includes('bg-primary/15');
  // Masks and presets persist to the fixture/settings — start clean so the
  // surface is re-runnable.
  mw.setUserPresets([]);
  mw.esUpdate({ masks: [] });
  mw.esCommit();
  await sleep(300);
  // A range mask is the cheapest smart mask: content-relative, so it travels
  // as a recipe, but it needs no model on disk.
  mw.esAddMask('range');
  await sleep(300);
  const mi = (es.getState().draft.masks?.length ?? 1) - 1;
  mw.esUpdateMask(mi, { adjust: { expEV: 0.8 } });
  mw.esCommit();
  await sleep(400);
  // A distinctly non-neutral look, so "the look didn't move" means something.
  mw.esUpdate({ contrast: 0.42, vibrance: 0.31 });
  mw.esCommit();
  await sleep(400);

  ui().setDevelopTab('presets');
  await sleep(700);
  const entryLabel = btn('Save preset') ? 'Save preset' : btn('Save current look') ? 'Save current look' : null;
  btn(entryLabel)?.click();
  await sleep(300);
  // Smart masks default to ON now — probe before touching the chip.
  const masksChip = () =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.trim().startsWith('Smart masks'));
  const masksDefaultOn = chipOn(masksChip());
  const sectionsDefaultOn = ['Tone', 'Presence', 'White balance', 'Color', 'Effects', 'Detail'].every((g) =>
    chipOn(btn(g)),
  );
  // Turn every look group off: masks are all that's left to carry.
  for (const g of ['Tone', 'Presence', 'White balance', 'Color', 'Effects', 'Detail']) {
    btn(g)?.click();
    await sleep(60);
  }
  const sectionsAllOff = ['Tone', 'Presence', 'White balance', 'Color', 'Effects', 'Detail'].every(
    (g) => !chipOn(btn(g)),
  );
  setInput(document.querySelector('input[aria-label="Preset name"]'), 'UITEST masks only');
  await sleep(150);
  const saveBtn = btn('Save');
  const saveEnabled = !!saveBtn && !saveBtn.disabled;
  saveBtn?.click();
  let saved = null;
  try {
    saved = await until(() => ui().userPresets.find((p) => p.name === 'UITEST masks only'), 5000);
  } catch {
    saved = null;
  }

  // Apply it over a DIFFERENT, mask-free look and watch what moves.
  let lookBefore = null;
  let lookAfter = null;
  let masksLanded = 0;
  if (saved) {
    mw.esUpdate({ masks: [], contrast: -0.2, vibrance: 0 });
    mw.esCommit();
    await sleep(500);
    const d0 = es.getState().draft;
    lookBefore = { contrast: d0.contrast, vibrance: d0.vibrance, expEV: d0.expEV, clarity: d0.clarity, saturation: d0.saturation };
    mw.esApplyUserPreset(saved);
    try {
      await until(() => (es.getState().draft.masks?.length ?? 0) > 0, 15000);
    } catch { /* probe reports 0 */ }
    await sleep(600);
    const d1 = es.getState().draft;
    lookAfter = { contrast: d1.contrast, vibrance: d1.vibrance, expEV: d1.expEV, clarity: d1.clarity, saturation: d1.saturation };
    masksLanded = d1.masks?.length ?? 0;
  }
  const near = (a, b) => a != null && b != null && Math.abs(a - b) < 0.002;
  window.__presetMasksProbe = {
    masksDefaultOn,
    sectionsDefaultOn,
    sectionsAllOff,
    saveEnabled,
    savedOK: !!saved,
    // The stored shape: neutral look params marked relative, recipes aboard.
    savedMaskCount: saved?.params.masks?.length ?? 0,
    savedRelative: saved?.relative === true,
    savedContrastNeutral: saved ? saved.params.contrast === 0 : null,
    savedVibranceNeutral: saved ? saved.params.vibrance === 0 : null,
    savedBaseExpEV: saved?.baseExpEV ?? null,
    lookBefore,
    lookAfter,
    lookUnchanged:
      !!lookBefore &&
      !!lookAfter &&
      Object.keys(lookBefore).every((k) => near(lookBefore[k], lookAfter[k])),
    masksLanded,
    badgeIsMasks: [...document.querySelectorAll('span')].some((s) => s.textContent.trim() === 'masks'),
    // The store copy has been through the server, which re-marshals params:
    // enums come back in the server's spelling ("" not 'camera') and the
    // omitempty fields (lensMode &c.) drop out altogether. badgeIsMasks is the
    // real assertion — it renders off that copy, so it only turns true if
    // isMasksOnlyPreset survives the round-trip.
    storeCopyRelative: ui().userPresets.find((p) => p.name === 'UITEST masks only')?.relative ?? null,
    storeCopyMaskCount:
      ui().userPresets.find((p) => p.name === 'UITEST masks only')?.params.masks?.length ?? null,
  };
} else if (shot === 'suggestions') {
  // The scene-aware suggestion gallery in the Presets tab: waits for the
  // candidate cards and their live thumbnails, probes hover (loupe override
  // paints, draft untouched, leave reverts), then applies one candidate and
  // probes the labeled undo entry + the Amount scrubber arming.
  mw.setFeature('suggestions', true); // experimental, off by default
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  ui().setDevelopTab('presets');
  const cards = () =>
    [...document.querySelectorAll('button')].filter(
      (b) => (b.title || '').startsWith('Apply ') && b.title.includes('white balance'),
    );
  await until(() => cards().length >= 3);
  await until(() => cards().filter((c) => c.querySelector('img')).length >= 3, 60000);
  const labels = cards().map((c) => c.textContent.trim());
  const draftBefore = JSON.stringify(es.getState().draft);
  // React maps onMouseEnter/Leave from mouseover/mouseout.
  cards()[1].dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
  await sleep(700); // debounce + low-res frame
  const hoverSet = es.getState().hoverParams != null;
  const draftUntouched = JSON.stringify(es.getState().draft) === draftBefore;
  cards()[1].dispatchEvent(new MouseEvent('mouseout', { bubbles: true }));
  await sleep(300);
  const hoverCleared = es.getState().hoverParams == null;
  const applyLabel = labels[1];
  cards()[1].click();
  await until(() => es.getState().lastPresetApply != null);
  await sleep(400);
  const st = es.getState();
  const h = st.history[st.photoId];
  window.__suggestProbe = {
    labels,
    hoverSet,
    draftUntouched,
    hoverCleared,
    appliedName: st.lastPresetApply?.name,
    historyLabel: h?.stack[h.index]?.label,
    labelMatches: st.lastPresetApply?.name === applyLabel && h?.stack[h.index]?.label === applyLabel,
    amountShown: !!st.lastPresetApply,
  };
} else if (shot === 'features' || shot === 'features-exp') {
  // Settings → Features at defaults: culling aids on (with the burst sliders
  // under the Bursts row), ML suggestions off wearing its Experimental badge
  // (features-exp scrolls the pane to it). Persisted setFeature writes seed
  // the defaults for idempotence.
  for (const id of ['bursts', 'softFilter', 'eyes', 'subjects']) mw.setFeature(id, true);
  mw.setFeature('suggestions', false);
  await sleep(500);
  ui().setSettingsOpen(true);
  await sleep(300);
  [...document.querySelectorAll('button')].find((b) => b.textContent === 'Features')?.click();
  await sleep(500);
  const dlg = document.querySelector('[role="dialog"]');
  const switches = [...dlg.querySelectorAll('[role="switch"]')];
  if (shot === 'features-exp') {
    const pane = dlg.querySelector('.overflow-y-auto');
    pane.scrollTop = pane.scrollHeight;
    await sleep(300);
  }
  window.__featuresProbe = {
    switchCount: switches.length,
    onCount: switches.filter((s) => s.getAttribute('aria-checked') === 'true').length,
    badge: dlg.textContent.includes('Experimental'),
    burstSliders: !!dlg.querySelector('[aria-label="Burst grouping sensitivity"]'),
  };
} else if (shot === 'features-off') {
  // Culling aids disabled: Soft/Bursts/Blinks/Auto-judge/Subjects/Eyes leave
  // the FilterBar, the grid badges go, and a blinks filter flipped on while
  // disabled (the other-window race) leaves the visible set untouched.
  // setState on the mirror, not setFeature — nothing persists past this run.
  const badgeCount = (t) => document.querySelectorAll(`[data-testid="${t}"]`).length;
  const badgesBefore = badgeCount('burst-badge');
  mw.useUIStore.setState({
    features: { bursts: false, softFilter: false, eyes: false, subjects: false },
  });
  await sleep(500);
  const byLabel = (l) =>
    [...document.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === l);
  const byTestId = (t) => document.querySelector(`[data-testid="${t}"]`);
  const before = ui().visibleIds.length;
  mw.useUIStore.setState({ eyesClosedOnly: true });
  await sleep(400);
  const blinkInert = ui().visibleIds.length === before;
  mw.useUIStore.setState({ eyesClosedOnly: false });
  window.__featuresProbe = {
    softBtn: !!byLabel('Show only soft-focus frames'),
    burstsBtn: !!byLabel('Collapse bursts to their sharpest frame'),
    blinksBtn: !!byTestId('blinks-filter'),
    autoJudgeBtn: !!byTestId('auto-judge-bursts'),
    subjectsBtn: !!byTestId('subject-scan-button'),
    eyesBtn: !!byTestId('eye-scan-button'),
    burstBadgesBefore: badgesBefore,
    burstBadges: badgeCount('burst-badge'),
    softBadges: badgeCount('soft-badge'),
    blinkInert,
    visible: before,
  };
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
} else if (shot === 'libpanel' || shot === 'libpanel-batch') {
  // The Library aside: info only for one photo (no tab strip, no navigator —
  // there is no loupe image behind the grid to navigate), and the batch stack
  // for several (relative sliders, the whole-selection paste/restore pair,
  // presets without the clipboard/history sections). The Develop drawer keeps
  // its tabs and its navigator, so probe that too.
  ui().setMode('library');
  ui().setView('grid');
  mw.setEditPanelHidden(false);
  await sleep(600);
  const aside = () => document.querySelector('aside.w-\\[300px\\]');
  // Match on leaf text: the info rows are dt/dd and the flag buttons wrap a
  // shortcut span, so a whole-subtree textContent comparison misses both.
  const texts = (root) =>
    [...(root?.querySelectorAll('*') ?? [])]
      .filter((n) => ![...n.children].some((c) => c.textContent?.trim()))
      .map((n) => n.textContent?.trim());
  const has = (root, t) => texts(root).some((s) => s === t);
  await until(() => aside());

  const single = aside();
  const singleProbe = {
    tabs: has(single, 'Develop') || has(single, 'Curve') || has(single, 'Presets'),
    navigator: has(single, 'Navigator'),
    histogram: has(single, 'Histogram'),
    info: has(single, 'Resolution') && has(single, 'File size'),
  };

  // The develop drawer must be untouched: tabs present, Info tab keeps the map.
  ui().setMode('develop');
  const drawer = await until(() =>
    [...document.querySelectorAll('.glass')].find((n) => has(n, 'Presets')),
  );
  mw.useUIStore.getState().setDevelopTab('info');
  await sleep(600);
  const developProbe = {
    tabs: has(drawer, 'Develop') && has(drawer, 'Presets') && has(drawer, 'Info'),
    navigator: has(drawer, 'Navigator'),
  };
  mw.useUIStore.getState().setDevelopTab('develop');
  ui().setMode('library');
  ui().setView('grid');
  await sleep(500);

  // Now several photos: the batch stack.
  const ids = ui().visibleIds;
  ui().focus(ids[2]);
  for (const id of ids.slice(3, 9)) ui().focus(id, { toggle: true });
  await sleep(900);
  const batch = aside();
  const batchProbe = {
    sliders: has(batch, 'Exposure') && has(batch, 'Contrast') && has(batch, 'Saturation'),
    paste: has(batch, 'Paste settings'),
    restore: has(batch, 'Restore original'),
    presets: has(batch, 'Auto everything') && has(batch, 'Creative presets') && has(batch, 'My presets'),
    appliesBadge: texts(batch).some((s) => /^applies to \d+ photos$/.test(s ?? '')),
    clipboard: has(batch, 'Clipboard'),
    history: has(batch, 'History'),
    oldHint: texts(batch).some((s) => /Absolute edits\?/.test(s ?? '')),
  };
  // The SelectionBar keeps rating/flag/delete and loses the edit actions.
  const bar = [...document.querySelectorAll('main div')].find((n) => has(n, 'Esc to clear'));
  const btn = (root, t) =>
    [...(root?.querySelectorAll('button') ?? [])].some((b) => b.textContent?.trim().startsWith(t));
  const barProbe = {
    found: !!bar,
    pick: btn(bar, 'Pick'), // wraps a shortcut span, so match the leading text
    rate: has(bar, 'Rate'),
    paste: btn(bar, 'Paste settings'),
    restore: btn(bar, 'Restore original'),
  };
  // Everything in the batch panel — paste, restore, presets — fans out over
  // the session's apply targets, so they must cover the whole selection.
  batchProbe.applyIds = mw.useEditSession.getState().applyIds.length;
  batchProbe.selected = ui().selection.size;
  if (shot === 'libpanel') {
    ui().focus(ids[2]); // back to a single selection for the capture
    await sleep(700);
  }
  window.__libPanelProbe = { single: singleProbe, develop: developProbe, batch: batchProbe, bar: barProbe };
} else if (shot === 'info') {
  // The Info tab: the metadata list, the focus scores' shoot-relative meters
  // (they need the calibrate pass to have measured the folder, so wait for a
  // caption rather than a fixed sleep), and the identity actions.
  ui().setMode('develop');
  await sleep(800);
  mw.useUIStore.getState().setDevelopTab('info');
  const drawer = await until(() => [...document.querySelectorAll('.glass')].find((n) => /Resolution/.test(n.textContent ?? '')));
  const text = () => drawer.textContent ?? '';
  const btn = (label) => [...drawer.querySelectorAll('button')].some((b) => b.textContent?.trim() === label);
  await until(() => /sharper than|soft for this shoot/.test(text()), 60000).catch(() => null);
  // The histogram and navigator push the info list under the fold — scroll to
  // it so the capture shows the rows and the actions.
  const scroller = [...drawer.querySelectorAll('div')].find((n) => n.scrollHeight > n.clientHeight + 20);
  if (scroller) scroller.scrollTop = scroller.scrollHeight;
  await sleep(400);
  const rows = [...drawer.querySelectorAll('dt')].map((n) => n.textContent?.trim());

  // What each action actually hands off: stub the two sinks (the clipboard
  // and the Explorer bridge) and read back the argument, so a button that
  // fires with the wrong path can't pass as "it clicked".
  const copied = [];
  navigator.clipboard.writeText = (t) => {
    copied.push(t);
    return Promise.resolve();
  };
  // The preload bridge is frozen, so swap the whole object rather than
  // assigning onto it (a silent no-op that would run the real Explorer call).
  // The preload bridge is frozen AND non-configurable, so the reveal argument
  // cannot be intercepted from here — check the path the button hands it,
  // which is the only part of that call this panel owns.
  const { joinPath } = await import('/src/lib/library.ts');
  const revealed = joinPath(ui().folderPath, drawer.querySelector('dd')?.getAttribute('title') ?? '');
  const click = (label) => {
    const b = [...drawer.querySelectorAll('button')].find((n) => n.textContent?.trim() === label);
    b?.click();
    return !!b;
  };
  const clicked = {
    locate: click('Locate on disk'),
    copy: click('Copy'),
    copyFolder: click('Copy folder path'),
    copyName: click('Copy filename'),
  };
  await sleep(200);

  // Every frame's caption: the rank spans the shoot, and a soft frame must say
  // so rather than quoting a percentile.
  const captions = [];
  for (const id of ui().visibleIds) {
    ui().focus(id);
    await sleep(250);
    captions.push((text().match(/sharper than \d+%|soft for this shoot/) ?? ['none'])[0]);
  }

  // The two states this shoot has no example of: a frame under its own soft
  // cutoff, and a subject-aware score (nothing here has an AI matte). Forced
  // through the local-override map, then put back so the capture shows the
  // folder's real numbers.
  const focusId = ui().focusId;
  const realScore = Number(
    [...drawer.querySelectorAll('dd')].find((n) => /^\d+$/.test(n.textContent?.trim() ?? ''))?.textContent,
  );
  ui().applyLocal([focusId], { sharpness: 1, subjectSharpness: 2 });
  await sleep(400);
  const forced = {
    rows: [...drawer.querySelectorAll('dt')].map((n) => n.textContent?.trim()),
    captions: [...drawer.querySelectorAll('span')]
      .filter((n) => /sharper than|soft for this shoot/.test(n.textContent ?? ''))
      .map((n) => ({ text: n.textContent, amber: /amber/.test(n.className) })),
  };
  ui().applyLocal([focusId], { sharpness: realScore, subjectSharpness: undefined });
  await sleep(400);

  window.__infoProbe = {
    forced,
    rows,
    focusMeter: (text().match(/sharper than \d+%|soft for this shoot/) ?? [null])[0],
    // The focus labels must carry the "what does this number mean" tooltip.
    focusHint: [...drawer.querySelectorAll('dt')]
      .find((n) => n.textContent?.trim() === 'Focus score')
      ?.getAttribute('title')
      ?.slice(0, 60),
    clicked,
    revealed,
    copiedInfo: copied[0]?.split('\n').slice(0, 3),
    copiedFolder: copied[1],
    copiedName: copied[2],
    captions,
  };
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
} else if (shot === 'restore') {
  // Startup with no folder requested: the app reopens the one the daemon
  // remembers (ui:lastFolder). Run it after a normal shot has put a folder
  // in that memory; point it at a folder that no longer exists to take the
  // failure branch, which must land on the library, say so, and leave the
  // rail on screen even if it was collapsed (its toggle lives in the folder
  // toolbar, so a hidden rail there is a dead end).
  await until(() => ui().settingsLoaded);
  const remembered = ui().lastFolder;
  // Toasts expire on their own, so sample rather than read once at the end —
  // "no error was shown" and "the error came and went" look identical
  // otherwise. 20s covers a cold scan of the folder that does open.
  let toast = '';
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const seen = [...document.querySelectorAll('[data-sonner-toast]')]
      .map((t) => t.textContent)
      .join(' | ');
    if (seen) toast = seen;
    if (ui().folderPath != null || toast) break;
    await sleep(100);
  }
  await sleep(1500);
  window.__restoreProbe = {
    remembered,
    folderPath: ui().folderPath,
    reopened: remembered !== '' && ui().folderPath === remembered,
    photos: ui().visibleIds.length,
    railHidden: ui().railHidden,
    railOnScreen: !!document.querySelector('[data-testid="library-rail"]'),
    toast,
    welcomeMounted: [...document.querySelectorAll('h2')].some((h) =>
      /Welcome to marraw/.test(h.textContent ?? ''),
    ),
  };
} else if (shot === 'welcome') {
  // The landing page (library has shoots, none open) plus the What's-new
  // dialog that an update raises over it. The harness's opened folder
  // guarantees a root exists; stepping out of it lands on Welcome.
  // Pass an old version via ?seedLastSeen= to raise the dialog, or skip the
  // param to shoot whatever state the daemon holds. The dialog re-reads the
  // stored version whenever it changes, so seeding works at any point.
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
  // Portalled to the body, so this finds it whatever is behind it.
  const dialogOf = () => document.querySelector('[data-testid="whats-new"]');
  const dialog = dialogOf();
  const bullets = dialog ? dialog.querySelectorAll('li').length : 0;
  // Dismissing is what marks the version seen — quitting mid-read must bring
  // the news back. Check that, then seed the old version again so the capture
  // still shows the dialog (it re-reads the stored version when it changes).
  let dismissed = null;
  if (dialog && seed != null) {
    document.querySelector('[data-testid="whats-new-dismiss"]').click();
    await sleep(600);
    dismissed = { gone: !dialogOf(), lastSeen: ui().lastSeenVersion };
    mw.setLastSeenVersion(seed);
    await until(() => dialogOf(), 5000).catch(() => null);
  }
  window.__welcomeProbe = {
    dialogShown: !!dialog,
    bullets,
    dismissed,
    reraised: !!dialogOf(),
    // The version is only marked seen by dismissing — until then it still
    // reads as the seeded one.
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
} else if (shot === 'watermark-frame') {
  // Rect element + polaroid frame: create a watermark, add a Rectangle, and
  // enable the frame with a chin (through the store — the same state the
  // Switch and sliders drive). Probe: corners and chin show the frame
  // color, the photo area stays the dark placeholder.
  const btn = (label) =>
    [...document.querySelectorAll('button')].find((b) => b.textContent.trim() === label);
  ui().setWatermarkEditorOpen(true);
  await sleep(400);
  btn('New watermark')?.click();
  await sleep(300);
  btn('Rectangle')?.click();
  await sleep(300);
  {
    const st = ui();
    const wm = st.watermarks[st.watermarks.length - 1];
    if (wm) {
      mw.useUIStore.setState({
        watermarks: st.watermarks.map((w) =>
          w.id === wm.id
            ? { ...w, frame: { ...w.frame, enabled: true, widthPct: 5, bottomPct: 18 } }
            : w,
        ),
      });
    }
  }
  await sleep(800);
  // Bring the rect card (last in the element list) into the capture.
  const list = [...document.querySelectorAll('[role="dialog"] .overflow-y-auto')].pop();
  if (list) list.scrollTop = list.scrollHeight;
  await sleep(200);
  const canvas = document.querySelector('[role="dialog"] canvas');
  let frameDrawn = false;
  let scrimDrawn = false;
  if (canvas) {
    const ctx = canvas.getContext('2d');
    const px = (x, y) => ctx.getImageData(Math.round(x), Math.round(y), 1, 1).data;
    const whiteish = (d) => d[0] > 235 && d[1] > 235 && d[2] > 235;
    const corner = px(2, 2);
    const side = px(3, canvas.height / 2);
    // The chin sample sits above the 14%-tall rect scrim that darkens the
    // very bottom; the scrim sample sits inside it (grey: black 55% over
    // the white chin).
    const chin = px(canvas.width / 2, canvas.height * 0.82);
    const scrim = px(canvas.width / 2, canvas.height - 3);
    const photo = px(canvas.width / 2, canvas.height / 3);
    frameDrawn = whiteish(corner) && whiteish(side) && whiteish(chin) && !whiteish(photo);
    scrimDrawn = scrim[0] > 60 && scrim[0] < 200 && Math.abs(scrim[0] - scrim[2]) < 12;
  }
  window.__wmProbe = { frameDrawn, scrimDrawn, canvas: !!canvas };
} else if (shot === 'watermark-bar' || shot === 'watermark-bar-portrait') {
  // Short-edge rule regression probe: a bottom bar + text must keep their
  // height ratio between the Landscape and Portrait previews (heightPct and
  // sizePct both resolve against the short edge). The bar is forced solid
  // red so the probe can tell it from the white text ink.
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
  const textInput = document.querySelector('input[aria-label="Watermark text"]');
  if (textInput) setInput(textInput, 'by marras');
  btn('Rectangle')?.click();
  await sleep(300);
  {
    const st = ui();
    const wm = st.watermarks[st.watermarks.length - 1];
    if (wm) {
      // Bar first so the text draws on top of it, like the real use case.
      const styled = wm.elements.map((e) =>
        e.type === 'rect'
          ? { ...e, fill: 'solid', color: '#ff0000', opacity: 1 }
          : e.type === 'text'
            ? { ...e, sizePct: 8 }
            : e,
      );
      styled.sort((a, b) => (a.type === 'rect' ? -1 : 0) - (b.type === 'rect' ? -1 : 0));
      mw.useUIStore.setState({
        watermarks: st.watermarks.map((w) => (w.id === wm.id ? { ...w, elements: styled } : w)),
      });
    }
  }
  if (shot === 'watermark-bar-portrait') {
    await sleep(300);
    btn('Portrait')?.click();
  }
  await sleep(2500);
  const canvas = document.querySelector('[role="dialog"] canvas');
  let barPx = 0;
  let textPx = 0;
  if (canvas) {
    const ctx = canvas.getContext('2d');
    // Bar thickness: red rows in a left-edge column, clear of the text.
    const col = ctx.getImageData(4, 0, 1, canvas.height).data;
    for (let y = 0; y < canvas.height; y++) {
      if (col[y * 4] > 150 && col[y * 4 + 1] < 80 && col[y * 4 + 2] < 80) barPx++;
    }
    // Text ink height: white pixel row bounds in the right half.
    const half = Math.floor(canvas.width / 2);
    const w = canvas.width - half;
    const d = ctx.getImageData(half, 0, w, canvas.height).data;
    let minY = -1;
    let maxY = -1;
    for (let y = 0; y < canvas.height; y++) {
      for (let x = 0; x < w; x++) {
        const i = (y * w + x) * 4;
        if (d[i] > 225 && d[i + 1] > 225 && d[i + 2] > 225) {
          if (minY < 0) minY = y;
          maxY = y;
        }
      }
    }
    if (maxY >= 0) textPx = maxY - minY + 1;
  }
  window.__wmProbe = {
    barPx,
    textPx,
    barOverText: barPx && textPx ? +(barPx / textPx).toFixed(2) : null,
    canvas: !!canvas,
  };
} else if (shot === 'subjects' || shot === 'subjectscan') {
  // Library toolbar's subject-scan control (beside "Soft"). Hide the develop
  // panel for maximum toolbar width; on a 1500px window the @container is
  // still below the 1440px label tier, so the buttons render icon-only (the
  // probe's `label` reads textContent, which includes the hidden span).
  ui().setMode('library');
  ui().setView('grid');
  mw.setEditPanelHidden(true);
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
} else if (shot === 'blinks') {
  // Blinks filter (FilterBar): the fixture is faceless, so the real scan
  // leaves every frame analyzed but unflagged. Overrides then drive the
  // states: masking eyesAnalyzed must disable the button; a fake closed-eye
  // score on one photo must make the filter isolate exactly that frame (with
  // its ◡ badge); toggling off must restore the full grid.
  ui().setMode('library');
  ui().setView('grid');
  mw.setEditPanelHidden(true);
  await sleep(400);
  const btn = () => document.querySelector('[data-testid="blinks-filter"]');
  // Eye scores exist only after a user-initiated scan (the calibrate pass no
  // longer backfills) — run eyes-verify against this fixture first, then the
  // button leaves disabled as the persisted scores stream in.
  await until(() => btn() && !btn().disabled, 60000);
  const ids = ui().visibleIds;
  const total = ids.length;

  // Disabled probe: mask every frame's analysis via overrides → count 0.
  const mask = new Map(ids.map((id) => [id, { eyesAnalyzed: false }]));
  mw.useUIStore.setState({ overrides: mask });
  await sleep(300);
  const disabledWithoutScores = btn()?.disabled === true;

  // Flag one frame as a blink and filter down to it.
  mw.useUIStore.setState({
    overrides: new Map([[ids[0], { eyesClosed: 0.92, eyesAnalyzed: true }]]),
  });
  await sleep(300);
  btn()?.click();
  await sleep(400);
  const filtered = ui().visibleIds;
  const stateOn = ui().eyesClosedOnly === true;
  const badges = document.querySelectorAll('[data-testid="eyes-badge"]').length;
  btn()?.click();
  await sleep(400);
  const restored = ui().visibleIds.length;
  btn()?.click(); // back on for the capture
  await sleep(400);
  window.__blinksProbe = {
    total,
    disabledWithoutScores,
    stateOn,
    filteredCount: filtered.length,
    filteredIsFlagged: filtered.length === 1 && filtered[0] === ids[0],
    badges,
    restored,
  };
} else if (shot === 'scanlabels') {
  // FilterBar scan-button labels: the N/M fraction shows only while a folder
  // is PARTIALLY scanned — fully scanned folders show the plain word. Point
  // this at a folder whose eyes scan has completed (eyes-noauto-verify leaves
  // one behind) so the eyes-complete case rides real server data; overrides
  // then drive the subjects-complete and both-partial cases.
  ui().setMode('library');
  ui().setView('grid');
  mw.setEditPanelHidden(true);
  await sleep(400);
  const label = (tid) =>
    document.querySelector(`[data-testid="${tid}"]`)?.textContent?.trim() ?? null;
  const ids = ui().visibleIds;
  // Real state: eyes fully scanned server-side, subjects never scanned.
  const real = { eyes: label('eye-scan-button'), subjects: label('subject-scan-button') };
  // Fully scanned on both axes.
  mw.useUIStore.setState({
    overrides: new Map(ids.map((id) => [id, { eyesAnalyzed: true, subjectAnalyzed: true }])),
  });
  await sleep(300);
  const full = { eyes: label('eye-scan-button'), subjects: label('subject-scan-button') };
  // One frame unscanned on each axis → the fraction returns.
  mw.useUIStore.setState({
    overrides: new Map(ids.map((id, i) => [id, { eyesAnalyzed: i > 0, subjectAnalyzed: i > 0 }])),
  });
  await sleep(300);
  const part = { eyes: label('eye-scan-button'), subjects: label('subject-scan-button') };
  mw.useUIStore.setState({ overrides: new Map() });
  await sleep(300);
  window.__scanLabelsProbe = { total: ids.length, real, full, part };
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
} else if (shot === 'eyetoggle') {
  // Eye toggle: two masks (one hidden via the real eye button) and a spot
  // (hidden too), so the shot shows both row states; probes that the clicks
  // actually set/clear `disabled` in the draft.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200); // initial preview settles
  ui().setDevelopTab('masks');
  mw.esUpdate({ masks: [], spots: [] }); // idempotence: drop persisted local edits
  mw.esCommit();
  await sleep(300);
  mw.esAddMask('radial');
  await sleep(200);
  mw.esUpdateMask(0, { adjust: { expEV: 1.2, temp: 0.5 } });
  mw.esCommit();
  mw.esAddMask('linear');
  await sleep(200);
  mw.esUpdateMask(1, { adjust: { expEV: -1.5 } });
  mw.esCommit();
  mw.esSetHealing(true);
  const idx = mw.esBeginSpot({ cx: 0.5, cy: 0.5, radius: 0.035, sx: 0.62, sy: 0.55, feather: 0.5 });
  await mw.esFinishSpot(idx);
  mw.esSetHealing(false);
  await sleep(300);
  // Hide mask 1 and the spot through the real buttons.
  const hideButtons = () => [...document.querySelectorAll('[aria-label="Hide mask"], [aria-label="Hide spot"]')];
  await until(() => hideButtons().length >= 3, 10000);
  hideButtons()[1].click(); // mask 1's eye
  await sleep(200);
  document.querySelector('[aria-label="Hide spot"]')?.click();
  await until(() => mw.esPreviewSettled(), 30000).catch(() => {});
  await sleep(400);
  const d = es.getState().draft;
  window.__maskProbe = {
    maskDisabled: [!!d.masks?.[0]?.disabled, !!d.masks?.[1]?.disabled],
    spotDisabled: !!d.spots?.[0]?.disabled,
    showButtons: document.querySelectorAll('[aria-label="Show mask"], [aria-label="Show spot"]').length,
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
} else if (shot === 'healfill') {
  // Content-aware fill: a fill-mode spot (no source ring, no source dot) with
  // its ML patch generated — needs migan-1.onnx in the models dir; the ensure
  // pass runs the inference after the commit.
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1200); // initial preview settles
  mw.esUpdate({ spots: [] }); // idempotence: drop any persisted spots
  mw.esCommit();
  await sleep(300);
  mw.esSetHealing(true);
  mw.esSetSpotMode('fill');
  const idx = mw.esBeginSpot({ cx: 0.5, cy: 0.5, radius: 0.05, sx: 0, sy: 0, feather: 0.5 });
  await mw.esFinishSpot(idx);
  mw.esSetActiveSpot(idx);
  // The ensure pass (inference) runs after the commit; wait for it to drain.
  await until(() => es.getState().fillBusy.length === 0, 60000).catch(() => {});
  await until(() => mw.esPreviewSettled(), 30000).catch(() => {});
  await sleep(400);
  window.__healProbe = {
    healing: es.getState().healing,
    spotCount: es.getState().draft?.spots?.length ?? 0,
    mode: es.getState().draft?.spots?.[0]?.mode ?? '',
    overlay: !!document.querySelector('[data-testid="heal-overlay"]'),
    sourceDot: !!document.querySelector('[title="Move source"]'),
    destDot: !!document.querySelector('[title="Move fill"]'),
    consentOpen: !!document.querySelector('[data-testid="ai-model-dialog"]'),
    activeSpot: es.getState().activeSpot,
  };
  mw.esSetSpotMode('heal'); // leave the session default as found
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
} else if (shot === 'tiles') {
  // Tile depth: at 1:1 the loupe covers the visible part of the photo with
  // full-resolution tiles over the 2048 underlay. This asserts the geometry
  // rather than the look — the tile layer derives its viewport from the
  // scroll container, so a sign error or a missing slack offset shows up as
  // tiles that load but sit somewhere else. Panning re-probes, because the
  // interesting failure is "the first screenful is right, the next is not".
  ui().setMode('develop');
  const es = mw.useEditSession;
  await until(() => es.getState().draft != null);
  await sleep(1500); // fit preview settles
  ui().setLoupeZoom(1);
  // The tile set is rendered on demand (dwell + 1.5-2.5s per photo).
  const tileEls = () => [...document.querySelectorAll('img[src*="/tile/"]')];
  await until(() => tileEls().length > 0, 40000).catch(() => {});
  await sleep(2500); // tiles decode and fade in

  const scroller = document.querySelector('.no-scrollbar.overflow-auto');
  // The underlay fills the photo's box exactly; the tile layer's scaled grid
  // must land on the same rectangle or every tile is off by the same amount.
  const underlay = document.querySelector('main img, img.absolute.inset-0') ??
    [...document.querySelectorAll('img')].find((i) => !i.src.includes('/tile/') && i.src.includes('/img/'));
  const grid = document.querySelector('img[src*="/tile/"]')?.parentElement ?? null;
  const rectOf = (el) => {
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)];
  };
  // Coverage: sample the viewport and require a loaded tile under every point
  // that falls inside the photo's box. A gap or an offset fails this.
  const coverage = () => {
    const sc = scroller?.getBoundingClientRect();
    const box = underlay?.getBoundingClientRect();
    if (!sc || !box) return { probed: 0, covered: 0 };
    const loaded = tileEls().filter((t) => t.naturalWidth > 0).map((t) => t.getBoundingClientRect());
    let probed = 0;
    let covered = 0;
    for (let iy = 1; iy < 8; iy++) {
      for (let ix = 1; ix < 8; ix++) {
        const x = sc.left + (sc.width * ix) / 8;
        const y = sc.top + (sc.height * iy) / 8;
        if (x < box.left || x > box.right || y < box.top || y > box.bottom) continue;
        probed++;
        if (loaded.some((r) => x >= r.left && x <= r.right && y >= r.top && y <= r.bottom)) covered++;
      }
    }
    return { probed, covered };
  };

  const first = coverage();
  const tilesFirst = tileEls().length;
  const gridRect = rectOf(grid);
  const boxRect = rectOf(underlay);

  // Pan a screenful right and down and let the layer catch up.
  let panned = { probed: 0, covered: 0 };
  let tilesAfterPan = 0;
  if (scroller) {
    scroller.scrollLeft += scroller.clientWidth;
    scroller.scrollTop += scroller.clientHeight * 0.5;
    await sleep(3000);
    tilesAfterPan = tileEls().length;
    panned = coverage();
  }

  window.__tilesProbe = {
    zoom: ui().loupeZoom,
    tilesFirst,
    tilesAfterPan,
    // The grid rect must equal the underlay's box: same origin, same size.
    gridRect,
    boxRect,
    aligned:
      !!gridRect && !!boxRect && gridRect.every((v, i) => Math.abs(v - boxRect[i]) <= 1),
    coverFirst: first,
    coverAfterPan: panned,
    fullyCovered: first.probed > 0 && first.covered === first.probed &&
      panned.probed > 0 && panned.covered === panned.probed,
  };
}
// Let previews decode, then wake the chrome (capture fires on resolve).
// ?shotNoWake=1 leaves the auto-hiding chrome (filmstrip deck) hidden.
await sleep(3600);
if (!params.get('shotNoWake')) {
  window.dispatchEvent(new PointerEvent('pointermove', { clientX: 500, clientY: 300 }));
}
await sleep(400);
const probe =
  window.__originalProbe ??
  window.__autoCropProbe ??
  window.__blinksProbe ??
  window.__scanLabelsProbe ??
  window.__cullUndoProbe ??
  window.__healProbe ??
  window.__subjectProbe ??
  window.__wmProbe ??
  window.__neardupProbe ??
  window.__modelsProbe ??
  window.__pairingProbe ??
  window.__remoteProbe ??
  window.__updatesProbe ??
  window.__maskProbe ??
  window.__maskOrderProbe ??
  window.__maskRemoveProbe ??
  window.__lensProbe ??
  window.__bwProbe ??
  window.__presetsProbe ??
  window.__presetMasksProbe ??
  window.__suggestProbe ??
  window.__featuresProbe ??
  window.__cropProbe ??
  window.__naturalGridProbe ??
  window.__renderProbe ??
  window.__settleProbe ??
  window.__restoreProbe ??
  window.__welcomeProbe ??
  window.__folderViewProbe ??
  window.__exportCopyProbe ??
  window.__exportPresetsProbe ??
  window.__shareProbe ??
  window.__libPanelProbe ??
  window.__infoProbe ??
  window.__tilesProbe;
return probe ? { shot, ...probe } : shot;
