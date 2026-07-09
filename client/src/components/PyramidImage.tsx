import { useState } from 'react';

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
export function PyramidImage({
  src,
  alt = '',
  className,
  loading,
  onLoad,
}: {
  src: string;
  alt?: string;
  className?: string;
  loading?: 'lazy' | 'eager';
  onLoad?: () => void;
}) {
  const [failed, setFailed] = useState<ReadonlySet<string>>(() => new Set());
  const [lastGood, setLastGood] = useState<string | null>(null);

  const fallback = lastGood && !failed.has(lastGood) ? lastGood : null;
  const shown = failed.has(src) ? fallback : src;
  if (!shown) return null;

  return (
    <img
      src={shown}
      alt={alt}
      draggable={false}
      loading={loading}
      className={className}
      onLoad={() => {
        setLastGood(shown);
        onLoad?.();
      }}
      onError={() => setFailed((f) => new Set(f).add(shown))}
    />
  );
}
