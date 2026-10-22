import { useEffect } from 'react';

import { ConnectionError, type ApiClient } from '@/api/client';
import { openInEditor } from '@/api/edits';

// The editor holds the photo it shows open on the backend (Edits.OpenInEditor)
// for as long as it shows it. While held, the photo's full-resolution decode
// stays in memory, so the 1:1 view after an edit that leaves the RAW decode
// alone — crop, masks, healing, every tone and colour slider — comes back
// without a second demosaic, and the hold warms that decode at once.

// HOLD_DWELL_MS: a photo is held only once the user has stayed on it. Every
// hold starts a full-resolution decode, so stepping through photos in Develop
// must not start one per step.
const HOLD_DWELL_MS = 1000;
// A hold that ends while the connection is down is made again as soon as the
// client is connected again. One that ends otherwise (an error from the
// backend) is tried again after these waits, then given up on: an error that
// keeps coming (a photo gone, an edit the backend cannot read) would only
// repeat.
const HOLD_RETRY_MS = [5000, 30_000, 30_000];
// A hold that lasted longer than this before it ended starts the count over.
const RETRY_RESET_MS = 30_000;
// Without a connection-state feed, how often to look whether the client is
// connected again.
const RECONNECT_POLL_MS = 1000;

/**
 * holdPhoto holds photoId open in the editor, after HOLD_DWELL_MS, until the
 * returned function is called; a hold that ends otherwise is made again.
 */
export function holdPhoto(client: ApiClient, photoId: number): () => void {
  const ac = new AbortController();
  let timer = 0;
  let unsubscribe: (() => void) | null = null;
  let tries = 0;
  const connected = () => (typeof client.isConnected === 'function' ? client.isConnected() : true);
  const whenConnected = () => {
    if (typeof client.onStateChange === 'function') {
      unsubscribe = client.onStateChange((state) => {
        if (state !== 'connected' || ac.signal.aborted) return;
        unsubscribe?.();
        unsubscribe = null;
        hold();
      });
      return;
    }
    const poll = () => {
      if (ac.signal.aborted) return;
      if (connected()) hold();
      else timer = window.setTimeout(poll, RECONNECT_POLL_MS);
    };
    timer = window.setTimeout(poll, RECONNECT_POLL_MS);
  };
  const hold = () => {
    const began = Date.now();
    const ended = (err: unknown) => {
      if (ac.signal.aborted) return;
      if (err instanceof ConnectionError || !connected()) {
        console.warn(`editor: holding photo ${photoId} ended with the connection; holding again once connected`);
        whenConnected();
        return;
      }
      if (Date.now() - began > RETRY_RESET_MS) tries = 0;
      const wait = HOLD_RETRY_MS[tries];
      tries++;
      const why = err == null ? 'the backend let go' : err instanceof Error ? err.message : String(err);
      if (wait == null) {
        console.warn(`editor: gave up holding photo ${photoId} open (${why})`);
        return;
      }
      timer = window.setTimeout(hold, wait);
    };
    // It answers only when cancelled; an answer otherwise means the backend
    // let go, so it is held again like after an error.
    openInEditor(client, photoId, { signal: ac.signal }).then(
      () => ended(null),
      (err: unknown) => ended(err),
    );
  };
  timer = window.setTimeout(hold, HOLD_DWELL_MS);
  return () => {
    window.clearTimeout(timer);
    unsubscribe?.();
    ac.abort();
  };
}

/** useEditorHold holds photoId open in the editor while it is set. */
export function useEditorHold(client: ApiClient, photoId: number | null) {
  useEffect(() => {
    if (photoId == null) return;
    return holdPhoto(client, photoId);
  }, [client, photoId]);
}
