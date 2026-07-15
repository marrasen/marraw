import { useEffect, useRef, useState } from 'react';

// PyramidImage paints a /img rendition and never falls back to the browser's
// broken-image glyph.
//
// Committing an edit pushes the photo's new edit hash the moment it is
// persisted — before any rendition for that hash exists. The server renders a
// missing one on demand, but only while the hash is still the photo's current
// state (imghttp.generatable): a request that lands after the *next* commit
// asks for a state nothing can reproduce and gets a 404. Edits in quick
// succession do exactly that, and a thumbnail queued behind the browser's
// six-connection limit can miss its window on a single commit.
//
// So a 404 here means "superseded", not "gone", and it is permanent for that
// URL: keep painting the last frame that loaded and wait. The newest commit's
// hash is always current, so the patch carrying it heals the thumbnail. With no
// frame to fall back on, nothing renders and the container's own placeholder
// (skeleton, inset panel) shows through.
//
// That permanent-failure rule is wrong for grid thumbnails on folder open,
// though: their URL is stable (no edit is superseding it), yet an on-demand
// render can transiently fail — a 500 or an aborted/evicted render — while the
// scan/calibrate/pre-render passes saturate the decode pool. Nothing pushes a
// per-image "ready" event, so a cell that errors once stays on the skeleton
// until an unrelated remount re-requests it. Callers that want a stable URL to
// self-heal pass `retry`: on error we re-request the same rendition a bounded
// number of times with backoff (a nonce forces the <img> to actually refetch),
// and only treat the URL as gone once the attempts run out.
export function PyramidImage({
  src,
  alt = '',
  className,
  loading,
  onLoad,
  retry = 0,
}: {
  src: string;
  alt?: string;
  className?: string;
  loading?: 'lazy' | 'eager';
  onLoad?: () => void;
  retry?: number;
}) {
  const [failed, setFailed] = useState<ReadonlySet<string>>(() => new Set());
  const [lastGood, setLastGood] = useState<string | null>(null);
  // Retries of the CURRENT src. >0 adds a cache-busting nonce so the browser
  // refetches — re-setting an identical src never reloads.
  const [attempt, setAttempt] = useState(0);
  const [attemptSrc, setAttemptSrc] = useState(src);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // A new src is a fresh subject: start attempts over (reset-on-prop-change
  // during render, per the React docs). The effect below cancels any pending
  // retry so a stale timer can't bump the new src's attempt count.
  if (attemptSrc !== src) {
    setAttemptSrc(src);
    setAttempt(0);
  }

  // Cancel a pending retry when src changes or the component goes away.
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [src],
  );

  const fallback = lastGood && !failed.has(lastGood) ? lastGood : null;
  const shown = failed.has(src) ? fallback : src;
  if (!shown) return null;

  // Bust only the live src, never a fallback frame we're merely holding.
  const url = shown === src && attempt > 0 ? `${src}&retry=${attempt}` : shown;

  return (
    <img
      src={url}
      alt={alt}
      draggable={false}
      loading={loading}
      className={className}
      onLoad={() => {
        setLastGood(url);
        onLoad?.();
      }}
      onError={() => {
        if (shown === src && attempt < retry) {
          // Transient failure under folder-open render load: re-request after a
          // backoff (~400ms → capped 6s) instead of stranding the cell.
          const delay = Math.min(6000, 400 * 2 ** attempt);
          if (timer.current) clearTimeout(timer.current);
          timer.current = setTimeout(() => setAttempt((n) => n + 1), delay);
        } else {
          setFailed((f) => new Set(f).add(shown));
        }
      }}
    />
  );
}
