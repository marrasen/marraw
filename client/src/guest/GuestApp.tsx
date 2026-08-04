import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { Download, Loader2, Star, X } from 'lucide-react';

import { useApiClient } from '@/api/client';
import { setFlag, setFocus, setRating, setVisible, useListPhotos } from '@/api/library';
import type { FlagType, Photo } from '@/api/library';
import type { FlagFilterType } from '@/api/settings';
import { useSession } from '@/api/share';
import type { GuestSession } from '@/api/share';
import { cn } from '@/lib/utils';

import { connection } from './connection';
import { Grid } from './Grid';
import { Loupe } from './Loupe';
import { saveSelection } from './save';

export function GuestApp() {
  const rejected = useSyncExternalStore(connection.subscribe, connection.get);
  const session = useSession();

  if (rejected) return <Message title="Link not available" body={rejected} />;
  if (session.error) {
    return (
      <Message
        title="Link not available"
        body="This share link has expired or been withdrawn. Ask for a new one."
      />
    );
  }
  if (!session.data) return <Message title="" body="" spinner />;
  return <Album session={session.data} />;
}

type Override = { rating?: number; flag?: FlagType };

function Album({ session }: { session: GuestSession }) {
  const client = useApiClient();
  const photos = useListPhotos(session.folderId);
  // The app's own filter vocabulary (see lib/usePhotos), defaulting to hiding
  // rejects: someone handed a shoot to pick from should not have to wade back
  // through the frames already thrown out.
  const [flagFilter, setFlagFilter] = useState<FlagFilterType>('not-excluded');
  const [minRating, setMinRating] = useState(0);
  // The loupe tracks the photo by id, not by position. Positions shift under
  // it whenever a filter or a rating changes; an id does not.
  const [openId, setOpenId] = useState<number | null>(null);
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());

  // Ratings applied locally the instant they are tapped. The server answers
  // with a patch, but over a funnel on mobile data that round trip is long
  // enough to feel like the tap missed.
  const [overrides, setOverrides] = useState<Record<number, Override>>({});
  // Drop an override once the server agrees, so a change the owner makes in
  // the app is not masked by a stale local value. Adjusted during render
  // rather than in an effect: an effect would paint one frame of the stale
  // value first, which is the exact flicker the overrides exist to avoid.
  const [reconciled, setReconciled] = useState<Photo[] | null>(null);
  if (photos.data && photos.data !== reconciled) {
    setReconciled(photos.data);
    setOverrides((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const p of photos.data ?? []) {
        const o = next[p.id];
        if (!o) continue;
        if ((o.rating ?? p.rating) === p.rating && (o.flag ?? p.flag) === p.flag) {
          delete next[p.id];
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }

  const all = useMemo(
    () => (photos.data ?? []).map((p) => (overrides[p.id] ? { ...p, ...overrides[p.id] } : p)),
    [photos.data, overrides],
  );
  const shown = useMemo(
    () =>
      all.filter((p) => {
        // The photo the loupe is on always stays in the list. Otherwise
        // tapping Reject while rejects are hidden would make the frame vanish
        // mid-look and slide the next one under the viewer's thumb.
        if (p.id === openId) return true;
        if (p.rating < minRating) return false;
        switch (flagFilter) {
          case 'pick':
            return p.flag === 'pick';
          case 'not-excluded':
            return p.flag !== 'exclude';
          default:
            return true;
        }
      }),
    [all, flagFilter, minRating, openId],
  );

  const rate = useCallback(
    (id: number, rating: number) => {
      setOverrides((o) => ({ ...o, [id]: { ...o[id], rating } }));
      void setRating(client, [id], rating);
    },
    [client],
  );
  const flag = useCallback(
    (id: number, value: FlagType) => {
      setOverrides((o) => ({ ...o, [id]: { ...o[id], flag: value } }));
      void setFlag(client, [id], value);
    },
    [client],
  );

  // Resolve the open photo to a position in the current list. A photo that has
  // disappeared entirely (deleted in the app) closes the loupe rather than
  // stranding it on an index that no longer means anything.
  const found = openId === null ? -1 : shown.findIndex((p) => p.id === openId);
  const loupeIndex = found === -1 ? null : found;

  // Tell the daemon what is on screen so the renditions ahead of the swipe are
  // warm. Fire-and-forget, and worth it: an unwarmed frame is a RAW decode.
  useEffect(() => {
    if (!shown.length) return;
    if (loupeIndex === null) {
      void setVisible(client, session.folderId, shown.slice(0, 24).map((p) => p.id));
      return;
    }
    const around = shown.slice(Math.max(0, loupeIndex - 2), loupeIndex + 4).map((p) => p.id);
    void setVisible(client, session.folderId, around);
    void setFocus(client, session.folderId, shown[loupeIndex].id);
  }, [client, session.folderId, shown, loupeIndex]);

  const toggleSelect = useCallback((id: number) => {
    setSelected((s) => {
      const next = new Set(s);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  }, []);

  if (photos.error) {
    return <Message title="Could not load the album" body="The link may have been withdrawn." />;
  }
  if (!photos.data) return <Message title="" body="" spinner />;

  return (
    <div className="flex min-h-[100dvh] flex-col">
      <header className="sticky top-0 z-[5] border-b border-white/10 bg-zinc-950/90 px-3 pt-[max(0.5rem,env(safe-area-inset-top))] pb-2 backdrop-blur">
        <div className="flex items-center gap-2">
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-base font-semibold">{session.name}</h1>
            <p className="text-xs text-white/50">
              {all.length} photo{all.length === 1 ? '' : 's'}
              {session.caps.cull ? ' · tap to rate and pick' : ''}
            </p>
          </div>
          {session.caps.downloads && (
            <button
              type="button"
              onClick={() => {
                setSelecting((s) => !s);
                setSelected(new Set());
              }}
              className="rounded-full border border-white/15 px-3 py-1.5 text-sm active:bg-white/10"
            >
              {selecting ? 'Cancel' : 'Select'}
            </button>
          )}
        </div>
        <div className="mt-2 flex items-center gap-1.5 overflow-x-auto">
          {FLAG_CHIPS.map((c) => (
            <Chip key={c.value} active={flagFilter === c.value} onClick={() => setFlagFilter(c.value)}>
              {c.label}
            </Chip>
          ))}
          <span className="mx-0.5 h-5 w-px shrink-0 bg-white/15" />
          {/* Minimum rating, same rule as the app's filter bar: tapping the
              star you are already on clears the filter. */}
          <div className="flex shrink-0 gap-px" role="group" aria-label="Minimum rating filter">
            {[1, 2, 3, 4, 5].map((n) => (
              <button
                key={n}
                type="button"
                aria-label={`Show ${n} stars and up`}
                aria-pressed={n <= minRating}
                onClick={() => setMinRating(minRating === n ? 0 : n)}
                className="p-1"
              >
                <Star
                  className={cn(
                    'size-4',
                    n <= minRating ? 'fill-amber-400 text-amber-400' : 'fill-white/20 text-transparent',
                  )}
                />
              </button>
            ))}
          </div>
        </div>
      </header>

      {shown.length === 0 ? (
        <p className="p-8 text-center text-sm text-white/50">
          {flagFilter === 'pick'
            ? 'Nothing picked yet.'
            : minRating > 0
              ? 'No photos at that rating.'
              : 'This album is empty.'}
        </p>
      ) : (
        <Grid
          photos={shown}
          selecting={selecting}
          selected={selected}
          onOpen={(i) => setOpenId(shown[i].id)}
          onToggleSelect={toggleSelect}
        />
      )}

      {selecting && selected.size > 0 && (
        <div className="sticky bottom-0 border-t border-white/10 bg-zinc-950/95 p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
          <button
            type="button"
            onClick={() => saveSelection([...selected])}
            className="flex w-full items-center justify-center gap-2 rounded-full bg-white py-3 font-medium text-black active:bg-white/80"
          >
            <Download className="size-5" />
            Download {selected.size} photo{selected.size === 1 ? '' : 's'}
          </button>
        </div>
      )}

      {loupeIndex !== null && (
        <Loupe
          photos={shown as Photo[]}
          index={loupeIndex}
          canDownload={session.caps.downloads}
          onIndex={(i) => setOpenId(shown[i]?.id ?? null)}
          onClose={() => setOpenId(null)}
          onRate={session.caps.cull ? rate : noop}
          onFlag={session.caps.cull ? flag : noop}
        />
      )}
    </div>
  );
}

const noop = () => {};

// Worded as the app words them (components/FilterBar), so the owner and the
// guest are talking about the same thing when they compare notes.
const FLAG_CHIPS: { value: FlagFilterType; label: string }[] = [
  { value: 'not-excluded', label: 'Not excluded' },
  { value: 'pick', label: 'Picks' },
  { value: 'all', label: 'All' },
];

function Chip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex items-center gap-1 rounded-full px-3 py-1 text-sm',
        active ? 'bg-white text-black' : 'bg-white/10 text-white/70 active:bg-white/20',
      )}
    >
      {children}
    </button>
  );
}

function Message({ title, body, spinner }: { title: string; body: string; spinner?: boolean }) {
  return (
    <div className="grid min-h-[100dvh] place-items-center p-8 text-center">
      <div>
        {spinner ? (
          <Loader2 className="mx-auto size-6 animate-spin text-white/40" />
        ) : (
          <>
            <X className="mx-auto mb-3 size-8 text-white/30" />
            <h1 className="text-lg font-semibold">{title}</h1>
            <p className="mt-1 text-sm text-white/50">{body}</p>
          </>
        )}
      </div>
    </div>
  );
}
