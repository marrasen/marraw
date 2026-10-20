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
}

export const useLoupeWindow = create<{ win: LoupeWindow | null }>(() => ({ win: null }));

/** What the loupe wants a window of. */
export interface WindowAsk {
  photoId: number;
  params: Params;
  rect: [number, number, number, number];
  dims: [number, number];
}

// Past this the view is zoomed out far enough for the drafts to do.
export const WINDOW_MAX_PIXELS = 8 << 20;

// again: the same window was wanted again while it rendered, so a null
// answer is asked once more (a save's decode may be ready by then).
let inFlight: { key: string; abort: AbortController; again: boolean } | null = null;
let wanted: WindowAsk | null = null;
// The key of the window showing: the same one is not asked for again.
let shownKey: string | null = null;
// Counts the windows let go: an answer from before one is dropped.
let gen = 0;

function keyOf(a: WindowAsk): string {
  return JSON.stringify([a.photoId, a.rect, a.dims, a.params]);
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
    return;
  }
  wanted = a;
  if (!inFlight) void render(client);
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
  inFlight = { key, abort, again: false };
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
    useLoupeWindow.setState({ win: { photoId: a.photoId, url: URL.createObjectURL(blob), rect: a.rect, dims: a.dims } });
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
}
