// The sharp window over a zoomed-in edit: the part of the photo in view at
// full resolution (Edits.PreviewWindow), while a slider moves and until the
// edit's tiles are in. Zoomed in, a draft of the whole frame is an upscale too
// soft to judge an edit by.
//
// Like the preview scheduler it renders one at a time: while one is in flight
// only the newest wanted window is remembered, and goes out when it returns.
// The backend answers null when it cannot make a window — no full-resolution
// decode kept under the edit's white balance, dehaze, mask effects — and the
// window then goes, so the drafts show rather than a window of an edit before.

import { create } from 'zustand';

import type { ApiClient } from '@/api/client';
import type { Params } from '@/api/edit';
import { previewWindow } from '@/api/edits';

/** A window on screen: rect is [x, y, w, h] in the full render's pixels. */
export interface LoupeWindow {
  photoId: number;
  url: string;
  rect: [number, number, number, number];
  // The full render's size the window was asked in: it shows only while
  // the loupe lays the photo out at that size.
  dims: [number, number];
  // The params it shows, as JSON: a window of other params than the photo
  // shows now is an earlier edit's (see LoupeView).
  paramsKey: string;
}

// lastWasDrag: the edit shown before the one shown now was a drag frame,
// on screen under DRAG_FRAME_MS (noteShownEdit). Its window shows on over
// the new edit until that one's comes; a window of an edit the photo had
// settled on does not — a one-shot change shows its preview at once.
export const useLoupeWindow = create<{ win: LoupeWindow | null; lastWasDrag: boolean }>(() => ({
  win: null,
  lastWasDrag: false,
}));

// How long an edit may stay on screen and still count as a drag frame.
const DRAG_FRAME_MS = 400;
let shownEdit = { key: '', since: 0, hovering: false };

/**
 * Note the edit the loupe shows now, as JSON, for lastWasDrag. Going into
 * or out of a hovered preset is never a drag: the look it leaves is not
 * the one coming, however briefly it showed.
 */
export function noteShownEdit(key: string, hovering: boolean) {
  if (key === shownEdit.key) return;
  const now = performance.now();
  const drag = now - shownEdit.since < DRAG_FRAME_MS && !hovering && !shownEdit.hovering;
  shownEdit = { key, since: now, hovering };
  useLoupeWindow.setState({ lastWasDrag: drag });
}

/** What the loupe wants a window of. */
export interface WindowAsk {
  photoId: number;
  params: Params;
  // The photo's pixels other than its edit — its fill patches and AI maps —
  // change under the same params; this counts them (useImgBust), so a fill
  // made after its window asks for a new one.
  bust: number;
  rect: [number, number, number, number];
  dims: [number, number];
}

// Past this the view is zoomed out far enough for the drafts to do.
export const WINDOW_MAX_PIXELS = 8 << 20;

// again: the same window was wanted again while it rendered, so a null
// answer is asked once more (a save's decode may be ready by then).
let inFlight: { key: string; ask: WindowAsk; abort: AbortController; again: boolean } | null = null;
let wanted: WindowAsk | null = null;
// The key of the window showing: the same one is not asked for again.
let shownKey: string | null = null;
// Counts the windows let go: an answer from before one is dropped.
let gen = 0;

function keyOf(a: WindowAsk): string {
  return JSON.stringify([a.photoId, a.bust, a.rect, a.dims, a.params]);
}

/**
 * Ask for a window of a, after the one under way if there is one. The same
 * window showing or rendering is not asked for again.
 */
export function wantWindow(client: ApiClient, a: WindowAsk) {
  const [, , w, h] = a.rect;
  if (w <= 0 || h <= 0 || w * h > WINDOW_MAX_PIXELS) {
    dropWindow();
    return;
  }
  const key = keyOf(a);
  if (key === shownKey || key === inFlight?.key) {
    wanted = null; // back to it: nothing else is wanted
    if (inFlight && key === inFlight.key) inFlight.again = true;
    else if (inFlight) {
      // Back to the window showing while another renders: that one would
      // land over it, an edit no longer shown.
      gen++;
      inFlight.abort.abort();
      inFlight = null;
    }
    return;
  }
  wanted = a;
  if (inFlight && sameEdit(inFlight.ask, a)) {
    // Only the view moved (a pan, a zoom's last frame): the window under
    // way is of a view no longer shown. The backend's decode, if it waits
    // on one, goes on without it.
    gen++;
    inFlight.abort.abort();
    inFlight = null;
  }
  if (!inFlight) void render(client);
}

function sameEdit(a: WindowAsk, b: WindowAsk): boolean {
  return a.photoId === b.photoId && a.bust === b.bust && JSON.stringify(a.params) === JSON.stringify(b.params);
}

/** Let the window go, and the one rendering. */
export function dropWindow() {
  gen++;
  wanted = null;
  shownKey = null;
  inFlight?.abort.abort();
  inFlight = null;
  const old = useLoupeWindow.getState().win;
  if (old) URL.revokeObjectURL(old.url);
  useLoupeWindow.setState({ win: null });
}

async function render(client: ApiClient) {
  const a = wanted;
  wanted = null;
  if (!a) return;
  const key = keyOf(a);
  const abort = new AbortController();
  const mine = gen;
  inFlight = { key, ask: a, abort, again: false };
  let blob: Blob | null = null;
  let failed = false;
  try {
    const [x, y, w, h] = a.rect;
    // Null at run time when the backend cannot make one (see the top).
    blob = (await previewWindow(client, a.photoId, a.params, x, y, w, h, { signal: abort.signal })) as Blob | null;
  } catch {
    failed = true; // let go, or a nicety that failed: the drafts show
  }
  if (mine !== gen) return; // let go meanwhile
  const again = inFlight?.again ?? false;
  inFlight = null;
  const old = useLoupeWindow.getState().win;
  if (blob && !failed) {
    if (old) URL.revokeObjectURL(old.url);
    shownKey = key;
    useLoupeWindow.setState({
      win: { photoId: a.photoId, url: URL.createObjectURL(blob), rect: a.rect, dims: a.dims, paramsKey: JSON.stringify(a.params) },
    });
  } else {
    // No window of this edit. It may come later (the backend decodes the
    // photo once the editor holds it, or waits for a save's decode), so
    // the same one may be asked again.
    if (old) URL.revokeObjectURL(old.url);
    shownKey = null;
    useLoupeWindow.setState({ win: null });
    if (again && !wanted) wanted = a;
  }
  if (wanted) void render(client);
}

/** For tests: forget everything. */
export function resetWindowForTest() {
  dropWindow();
  gen = 0;
  shownEdit = { key: '', since: 0, hovering: false };
  useLoupeWindow.setState({ lastWasDrag: false });
}
