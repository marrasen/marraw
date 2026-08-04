import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { Check, Download, Loader2, X } from 'lucide-react';

import { useApiClient } from '@/api/client';
import { setFlag, setFocus, setRating, setVisible, useListPhotos } from '@/api/library';
import type { FlagType, Photo } from '@/api/library';
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
type Filter = 'all' | 'picks';

function Album({ session }: { session: GuestSession }) {
  const client = useApiClient();
  const photos = useListPhotos(session.folderId);
  const [filter, setFilter] = useState<Filter>('all');
  const [openIndex, setOpenIndex] = useState<number | null>(null);
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
    () => (filter === 'picks' ? all.filter((p) => p.flag === 'pick') : all),
    [all, filter],
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

  // A filter change can strand the loupe past the end of the shorter list, so
  // clamp on the way out rather than storing an index that may not exist.
  const loupeIndex =
    openIndex === null || shown.length === 0 ? null : Math.min(openIndex, shown.length - 1);

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
        <div className="mt-2 flex gap-1.5">
          <Chip active={filter === 'all'} onClick={() => setFilter('all')}>
            All
          </Chip>
          <Chip active={filter === 'picks'} onClick={() => setFilter('picks')}>
            <Check className="size-3.5" /> Picks
          </Chip>
        </div>
      </header>

      {shown.length === 0 ? (
        <p className="p-8 text-center text-sm text-white/50">
          {filter === 'picks' ? 'Nothing picked yet.' : 'This album is empty.'}
        </p>
      ) : (
        <Grid
          photos={shown}
          selecting={selecting}
          selected={selected}
          onOpen={setOpenIndex}
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
          onIndex={setOpenIndex}
          onClose={() => setOpenIndex(null)}
          onRate={session.caps.cull ? rate : noop}
          onFlag={session.caps.cull ? flag : noop}
        />
      )}
    </div>
  );
}

const noop = () => {};

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
