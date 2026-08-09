import { useEffect, useRef, useState } from 'react';
import { useApiClient } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import { entriesSince, type ChangelogEntry } from '@/lib/changelog';
import { updateLastSeenVersion } from '@/lib/uiSettings';
import { useUIStore } from '@/stores/uiStore';

/**
 * "What's new" after an update: the mark, the version now running, and every
 * release since the one this machine last saw.
 *
 * It is a modal rather than a card on the Welcome page because the app no
 * longer reliably lands on Welcome — it reopens the folder you had open, and
 * the news would go unseen behind it. Dismissing is what marks the version
 * seen, so quitting mid-read brings it back next launch.
 *
 * Betas are part of the story: the changelog carries `X.Y.Z-beta.N` sections
 * and they order below the stable release, so a tester sees each beta's notes
 * and then the stable ones when they move onto it.
 */
export function WhatsNewDialog() {
  const client = useApiClient();
  const settingsLoaded = useUIStore((s) => s.settingsLoaded);
  const lastSeen = useUIStore((s) => s.lastSeenVersion);
  // Recomputed each time the stored version actually changes — once when
  // settings arrive, and again if another window (or a test harness) resets
  // it. `reacted` keeps that from re-running on unrelated renders, and makes
  // our own dismiss write a no-op rather than a second pass.
  const [entries, setEntries] = useState<ChangelogEntry[] | null>(null);
  const reacted = useRef<string | null>(null);
  useEffect(() => {
    if (!settingsLoaded || reacted.current === lastSeen) return;
    reacted.current = lastSeen;
    // '' is a fresh install — App baselines it silently, there is no news yet.
    const since = lastSeen === '' ? [] : entriesSince(lastSeen, __APP_VERSION__);
    setEntries(since);
    // Nothing to show (a build with no changelog section of its own, or a
    // downgrade): move the baseline anyway so the next update compares
    // against what is actually running.
    if (since.length === 0 && lastSeen !== '' && lastSeen !== __APP_VERSION__) {
      updateLastSeenVersion(client, __APP_VERSION__);
    }
  }, [settingsLoaded, lastSeen, client]);

  const dismiss = () => {
    setEntries([]);
    updateLastSeenVersion(client, __APP_VERSION__);
  };

  if (entries == null || entries.length === 0) return null;
  return (
    <Dialog open onOpenChange={(open) => !open && dismiss()}>
      <DialogContent showCloseButton={false} className="sm:max-w-lg" data-testid="whats-new">
        <div className="flex flex-col items-center gap-2 pt-1 text-center">
          {/* Relative URL: the packaged shell loads index.html over file://. */}
          <img src="./icon.svg" alt="" className="size-16" />
          <div className="font-heading text-base font-medium">marraw v{__APP_VERSION__}</div>
          <div className="text-xs text-muted-foreground">
            {entries.length > 1 ? `Updated — ${entries.length} releases of news` : 'Updated'}
          </div>
        </div>

        <div className="max-h-[46vh] overflow-y-auto rounded-lg border bg-card/50 px-4 py-3">
          {entries.map((e, ei) => (
            <div key={e.version} className={ei > 0 ? 'mt-4' : undefined}>
              {entries.length > 1 && (
                <div className="text-xs font-medium text-muted-foreground">
                  v{e.version}
                  {e.date && ` — ${e.date}`}
                </div>
              )}
              <ul className="mt-1.5 flex flex-col gap-1 text-sm text-secondary-foreground">
                {e.items.map((item, i) => (
                  <li key={i} className="flex gap-2">
                    <span className="text-faint">•</span>
                    <ChangeItem text={item} />
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        <Button className="w-full" onClick={dismiss} data-testid="whats-new-dismiss">
          Continue
        </Button>
      </DialogContent>
    </Dialog>
  );
}

// Changelog bullets follow the commit style "Area: what changed" — set the
// area off in a stronger weight when present.
function ChangeItem({ text }: { text: string }) {
  const sep = text.indexOf(': ');
  if (sep <= 0) return <span>{emphasized(text)}</span>;
  return (
    <span>
      <span className="font-medium text-foreground">{text.slice(0, sep)}:</span>
      {emphasized(text.slice(sep + 1))}
    </span>
  );
}

// The bullets are written as Markdown (they double as GitHub release notes),
// and **strong** / *emphasis* are the only constructs they use. Rendering
// those as literal asterisks is worse than not writing them, so honour the
// two. Splitting on a capture group puts the matches at the odd indices;
// strong is taken first so its pair of asterisks can't read as emphasis.
function emphasized(text: string) {
  return text
    .split(/\*\*(.+?)\*\*/gs)
    .map((part, i) =>
      i % 2 === 1 ? (
        <strong key={i} className="font-medium text-foreground">
          {part}
        </strong>
      ) : (
        italicized(part, i)
      ),
    );
}

function italicized(text: string, key: number) {
  return text
    .split(/\*(.+?)\*/gs)
    .map((part, i) => (i % 2 === 1 ? <em key={`${key}-${i}`}>{part}</em> : part));
}
