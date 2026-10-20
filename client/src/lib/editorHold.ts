import { useEffect } from 'react';

import type { ApiClient } from '@/api/client';
import { openInEditor } from '@/api/edits';

// The editor holds the photo it shows open on the backend (Edits.OpenInEditor)
// for as long as it shows it. While held, the photo's full-resolution decode
// stays in memory, so the 1:1 view after an edit that leaves the RAW decode
// alone — crop, masks, healing, every tone and colour slider — comes back
// without a second demosaic, and the hold warms that decode at once.

// HOLD_DWELL_MS: a photo is held only once the user has stayed on it, the
// same pause the 1:1 render waits for while skimming. Every hold starts a
// full-resolution decode, so stepping through photos must not start one per
// step.
const HOLD_DWELL_MS = 350;
// Waits before holding again after a hold ended with an error (a reconnect,
// a backend restart): quick at first, then every 30 s while still on the
// photo. A hold that lasted longer than RETRY_RESET_MS starts the count over.
const HOLD_RETRY_MS = [1000, 5000, 5000];
const HOLD_RETRY_LATER_MS = 30_000;
const RETRY_RESET_MS = 30_000;

let held: number | null = null;

/** The photo the editor holds open, or null. */
export function heldPhotoId(): number | null {
  return held;
}

/**
 * holdPhoto holds photoId open in the editor, after HOLD_DWELL_MS, until the
 * returned function is called; a hold that ends otherwise is made again.
 */
export function holdPhoto(client: ApiClient, photoId: number): () => void {
  const ac = new AbortController();
  let timer = 0;
  let tries = 0;
  const hold = () => {
    held = photoId;
    const began = Date.now();
    const ended = (why: string) => {
      if (ac.signal.aborted) return;
      if (Date.now() - began > RETRY_RESET_MS) tries = 0;
      const wait = HOLD_RETRY_MS[tries] ?? HOLD_RETRY_LATER_MS;
      tries++;
      console.warn(`editor: holding photo ${photoId} ended (${why}); trying again in ${wait / 1000} s`);
      timer = window.setTimeout(hold, wait);
    };
    // It answers only when cancelled; an answer otherwise means the backend
    // let go (a restart), so it is held again like after an error.
    openInEditor(client, photoId, { signal: ac.signal }).then(
      () => ended('the backend let go'),
      (err: unknown) => ended(err instanceof Error ? err.message : String(err)),
    );
  };
  timer = window.setTimeout(hold, HOLD_DWELL_MS);
  return () => {
    window.clearTimeout(timer);
    ac.abort();
    if (held === photoId) held = null;
  };
}

/** useEditorHold holds photoId open in the editor while it is set. */
export function useEditorHold(client: ApiClient, photoId: number | null) {
  useEffect(() => {
    if (photoId == null) return;
    return holdPhoto(client, photoId);
  }, [client, photoId]);
}
