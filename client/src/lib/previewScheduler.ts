// The preview render scheduler: everything between "the draft changed" and a
// JPEG on the loupe.
//
// Its state is module-level and deliberately single-instance — one loupe, one
// render in flight — which is exactly why it wanted a file of its own: mixed
// in with the tool-mode helpers and the history, the invariants below had to
// be reconstructed from a 2,000-line file every time anyone touched them.
// previewScheduler.test.ts pins them.
//
// Nothing here decides WHAT to draw; callers set the draft and say whether
// they want a drag frame or a settle.

import type { ApiClient } from '@/api/client';
import type { Params } from '@/api/edit';
import { previewEdit } from '@/api/edits';
import { useEditSession, type EditSessionState } from '@/lib/editSessionStore';

function setState(patch: Partial<EditSessionState> | ((s: EditSessionState) => Partial<EditSessionState>)) {
  useEditSession.setState(patch);
}

// Preview renders are coalesced, not debounced: a render fires immediately
// when none is in flight, and while one IS in flight only "a newer state is
// wanted" is remembered — the moment the current render returns, the newest
// draft goes out. The server paces the stream naturally and no fixed latency
// is added to any adjustment. Drag frames render at DRAFT_PX (quarter the
// pixels of 2048, in-memory on the server — fast and cheap, transiently
// upscaled by the loupe); commits and one-shot applies render FULL_PX, which
// the server also persists to the pyramid cache for /img. There is no settle
// timer anywhere: the sharp 2048 is queued immediately behind the instant
// low-res frame, and a further edit ABORTS a stale in-flight 2048 (the abort
// rides the WebSocket as a cancel frame and cancels the server handler's ctx)
// instead of waiting behind it.
const DRAFT_PX = 1024;
const FULL_PX = 2048;
// A render is keyed by the exact params it draws — crop-flattened while
// cropping — plus the flat tag itself: entering crop mode re-renders the same
// params as a different frame (the flat one), so the tag must participate.
type RenderKey = { params: string; flat: boolean };
// The single in-flight render. A full (2048) render is aborted when a newer
// edit supersedes it; in-flight 1024s are never aborted — fold frames are
// fast, land fresher feedback than a restart would, and the server can't
// cancel mid-decode anyway.
let inFlight: { full: boolean; abort: AbortController; key: RenderKey } | null = null;
// What to render next, remembered while a render is in flight. Two slots so
// a step/preset toggle that asks for a drag frame AND a settle in the same
// tick paints the instant 1024 first with the 2048 queued right behind it —
// a single sticky slot would render every held-key step at 2048.
let pending: { low: boolean; full: boolean } | null = null;
// The last FULL render that landed on screen: a settle request for the
// identical frame is skipped (the drag-release commit right after an
// identical settle, most commonly).
let lastShown: { photoId: number; key: RenderKey } | null = null;

// In crop mode the loupe draws the rectangle and applies the straighten angle
// as a CSS rotation, both client-side — so the backend renders the flat full
// frame (crop + angle stripped) and the overlay/transform do the rest. The
// draft keeps the real crop and angle for commit.
function flattenedParams(draft: Params, cropping: boolean): Params {
  return cropping ? { ...draft, cropX: 0, cropY: 0, cropW: 0, cropH: 0, cropAngle: 0 } : draft;
}

function keyFor(draft: Params, cropping: boolean): RenderKey {
  return { params: JSON.stringify(flattenedParams(draft, cropping)), flat: cropping };
}

function sameKey(a: RenderKey, b: RenderKey): boolean {
  return a.flat === b.flat && a.params === b.params;
}

// schedulePreview requests a render of the current draft. 'draft' is a drag
// frame: it REPLACES the pending slots (a manual edit supersedes any queued
// settle — the commit at drag end queues a fresh one) and aborts an in-flight
// full render, whose pixels are now stale. 'settle' queues the sharp 2048
// stickily behind whatever is running, aborting an in-flight full only when
// it renders different params.
export function schedulePreview(client: ApiClient, kind: 'draft' | 'settle') {
  if (!inFlight) {
    void renderPreview(client, kind === 'settle');
    return;
  }
  if (kind === 'draft') {
    pending = { low: true, full: false };
    if (inFlight.full) inFlight.abort.abort();
  } else {
    pending = { low: pending?.low ?? false, full: true };
    if (inFlight.full) {
      // The supersede comparison must see the freshest slider state — but
      // only this branch pays the flush, so drag frames keep their per-frame
      // draft coalescing.
      esFlushDraft();
      const s = useEditSession.getState();
      const shown = s.hoverParams ?? s.draft;
      if (shown && !sameKey(inFlight.key, keyFor(shown, s.cropping))) inFlight.abort.abort();
    }
  }
}

// Draft writes during a slider drag are coalesced to one per animation frame:
// the develop panel has ~30 controls, so applying every pointer-move
// synchronously re-rendered all of them (plus the loupe and histogram) and
// made dragging stutter. flushDraft merges the pending patch once per frame.
let pendingPatch: Partial<Params> | null = null;
let draftRaf = 0;
function flushDraft() {
  draftRaf = 0;
  const patch = pendingPatch;
  pendingPatch = null;
  if (!patch) return;
  const s = useEditSession.getState();
  if (s.draft) setState({ draft: { ...s.draft, ...patch } });
}
// esFlushDraft applies any frame-pending patch immediately — call before
// reading the draft for a commit so nothing in flight is lost.
export function esFlushDraft() {
  if (draftRaf) {
    cancelAnimationFrame(draftRaf);
    flushDraft();
  }
}

// esUpdate changes the draft (coalesced to a frame) and schedules a low-res
// live preview render (coalesced against the in-flight one).

async function renderPreview(client: ApiClient, full: boolean) {
  esFlushDraft(); // render the freshest slider state, not last frame's
  const { photoId, draft, hoverParams, cropping } = useEditSession.getState();
  if (photoId == null || !draft) return;
  // A hovered preset overrides what the loupe shows; the draft (and
  // everything keyed off it — persistence, history) is untouched.
  const shown = hoverParams ?? draft;
  const renderParams = flattenedParams(shown, cropping);
  const key = keyFor(shown, cropping);
  // The identical sharp frame already landed (and ensurePreview wrote it to
  // the pyramid cache) — nothing to render. Most commonly the drag-release
  // commit right after an identical settle.
  if (full && lastShown && lastShown.photoId === photoId && sameKey(lastShown.key, key)) return;
  const ac = new AbortController();
  inFlight = { full, abort: ac, key };
  setState((s) => ({ rendering: s.rendering + 1 }));
  try {
    const blob = await previewEdit(client, photoId, renderParams, full ? FULL_PX : DRAFT_PX, {
      signal: ac.signal,
    });
    if (useEditSession.getState().photoId !== photoId || ac.signal.aborted) return;
    const url = URL.createObjectURL(blob);
    const old = useEditSession.getState().preview;
    if (old) URL.revokeObjectURL(old.url);
    setState({ preview: { photoId, url, blob, flat: cropping } });
    // lastShown means "the displayed blob IS this sharp frame" — a low-res
    // frame replacing it on screen must clear it, or returning to the exact
    // same params would dedupe-skip the settle and leave the soft 1024 up.
    lastShown = full ? { photoId, key } : null;
  } catch {
    // aborted or superseded
  } finally {
    inFlight = null;
    // Fire the queued state now — even when this render was aborted: the
    // abort-on-supersede path wants its replacement immediately, and a photo
    // switch cleared `pending` before aborting so it never refires here. The
    // low frame goes first for instant feedback, keeping the settle queued
    // behind it. Refire BEFORE decrementing so `rendering` never touches 0
    // mid-handoff and esPreviewSettled stays false throughout.
    const p = pending;
    if (p) {
      pending = p.low && p.full ? { low: false, full: true } : null;
      void renderPreview(client, !p.low);
    }
    setState((s) => ({ rendering: Math.max(0, s.rendering - 1) }));
  }
}

// --- what editSession needs to ask about this machine ---

/** True when no render is in flight, queued, or owed a coalesced draft write. */
export function schedulerIdle(): boolean {
  return !inFlight && !pending && !pendingPatch;
}

/**
 * Forget that the displayed blob is a sharp frame. Called when the preview is
 * cleared: a later settle for the identical params must render rather than
 * dedupe-skip against a frame that is no longer on screen.
 */
export function forgetShown() {
  lastShown = null;
}

/**
 * Abandon everything for the outgoing photo. `pending` is cleared BEFORE the
 * abort so the aborted render's finally block cannot refire it against the
 * photo that just arrived.
 */
export function abandonRenders() {
  pending = null;
  inFlight?.abort.abort();
  lastShown = null;
}

/** Queue a draft patch for the next animation frame (see flushDraft). */
export function queueDraftPatch(patch: Partial<Params>) {
  pendingPatch = { ...(pendingPatch ?? {}), ...patch };
  if (!draftRaf) draftRaf = requestAnimationFrame(flushDraft);
}

