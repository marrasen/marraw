import { toast } from 'sonner';
import type { FlagType } from '@/api/library';
import type { ApiClient } from '@/api/client';
import type { BurstInfo } from '@/lib/bursts';
import { useUIStore } from '@/stores/uiStore';
import { chPlay, chPush, chRevert, type CullEntry } from '@/lib/cullHistory';

// Optimistic rating/flag mutations shared by the keyboard map and the
// edit-panel controls: record an undo entry capturing the prior values, apply
// the local override immediately, then persist. A failed persist reverts the
// entry so it doesn't sit in the history pretending it happened.

type Store = ReturnType<typeof useUIStore.getState>;

// Overrides-first: photoMeta is refreshed by a React effect after render,
// while applyLocal writes overrides synchronously — back-to-back actions must
// see the previous one's values.
const priorFlag = (s: Store, id: number): FlagType =>
  s.overrides.get(id)?.flag ?? s.photoMeta.get(id)?.flag ?? 'none';
const priorRating = (s: Store, id: number): number =>
  s.overrides.get(id)?.rating ?? s.photoMeta.get(id)?.rating ?? 0;

// Undo groups ids by their distinct prior value, so the replay is one bulk
// RPC per value instead of one per photo.
function groupByPrior<V>(ids: number[], prior: (id: number) => V): { ids: number[]; value: V }[] {
  const byValue = new Map<V, number[]>();
  for (const id of ids) {
    const v = prior(id);
    const list = byValue.get(v);
    if (list) list.push(id);
    else byValue.set(v, [id]);
  }
  return [...byValue].map(([value, groupIds]) => ({ ids: groupIds, value }));
}

function commit(client: ApiClient, entry: CullEntry, what: string) {
  chPush(entry);
  void chPlay(client, entry.redo).then((results) => {
    const bad = results.find((r): r is PromiseRejectedResult => r.status === 'rejected');
    if (bad) {
      chRevert(client, entry);
      toast.error(`${what} failed: ${bad.reason instanceof Error ? bad.reason.message : bad.reason}`);
    }
  });
}

const countSuffix = (n: number) => (n > 1 ? ` ${n} photos` : '');

const FLAG_LABELS: Record<FlagType, string> = { pick: 'Pick', exclude: 'Exclude', none: 'Unflag' };

// applyFlagOps applies several flag mutations as ONE undo entry — judgeBurst
// excludes the burst siblings and picks the kept frame in a single stroke,
// and a single Ctrl+Z must restore the whole burst. Ids already carrying the
// target flag are dropped, so a repeat press is a true no-op: nothing pushed,
// nothing sent.
export function applyFlagOps(
  client: ApiClient,
  ops: { ids: number[]; flag: FlagType }[],
  label?: string,
) {
  const s = useUIStore.getState();
  const eff = ops
    .map((o) => ({ ids: o.ids.filter((id) => priorFlag(s, id) !== o.flag), flag: o.flag }))
    .filter((o) => o.ids.length > 0);
  if (eff.length === 0) return;
  const touched = eff.flatMap((o) => o.ids);
  const entry: CullEntry = {
    label: label ?? `${FLAG_LABELS[eff[0].flag]}${countSuffix(touched.length)}`,
    redo: { kind: 'flag', groups: eff.map((o) => ({ ids: o.ids, value: o.flag })) },
    undo: { kind: 'flag', groups: groupByPrior(touched, (id) => priorFlag(s, id)) },
  };
  commit(client, entry, 'Flag');
}

export function applyFlag(client: ApiClient, ids: number[], flag: FlagType) {
  applyFlagOps(client, [{ ids, flag }]);
}

// judgeAllBursts sweeps every burst in the folder — the folder-wide Shift+P:
// pick each group's sharpest member, exclude the rest, so "show excluded"
// turns into a delete list. The whole sweep is ONE undo entry. A burst is
// left alone when it has no sharpness scores yet (nothing to judge by), or
// when the user already picked a non-sharpest member — an automatic sweep
// must not reverse a hand judgement (the sharpest frame may be the blink).
// Judges the WHOLE folder's bursts, not just the filtered view, like the
// burst grouping itself.
export function judgeAllBursts(client: ApiClient, bursts: Map<number, BurstInfo>) {
  const s = useUIStore.getState();
  const picks: number[] = [];
  const rejects: number[] = [];
  let handJudged = 0;
  let unscored = 0;
  for (const b of bursts.values()) {
    const best = b.bestId;
    if (best == null) {
      unscored++;
      continue;
    }
    if (b.members.some((id) => id !== best && priorFlag(s, id) === 'pick')) {
      handJudged++;
      continue;
    }
    picks.push(best);
    rejects.push(...b.members.filter((id) => id !== best));
  }
  // Effective counts for the toast — applyFlagOps drops already-matching
  // flags itself, but the message should say what actually changed.
  const newRejects = rejects.filter((id) => priorFlag(s, id) !== 'exclude').length;
  const newPicks = picks.filter((id) => priorFlag(s, id) !== 'pick').length;
  if (newRejects + newPicks === 0) {
    toast.info(bursts.size === 0 ? 'No bursts in this folder' : 'Every burst is already judged');
    return;
  }
  applyFlagOps(
    client,
    [
      { ids: rejects, flag: 'exclude' },
      { ids: picks, flag: 'pick' },
    ],
    'Auto-judge bursts',
  );
  const skipped = [
    handJudged > 0 ? `${handJudged} hand-judged` : '',
    unscored > 0 ? `${unscored} unscored` : '',
  ]
    .filter(Boolean)
    .join(', ');
  toast.success(
    `Judged ${picks.length} burst${picks.length === 1 ? '' : 's'}: picked the sharpest, rejected ${newRejects} frame${newRejects === 1 ? '' : 's'}${skipped ? ` — skipped ${skipped}` : ''}. Ctrl+Z undoes it.`,
  );
}

export function applyRating(client: ApiClient, ids: number[], rating: number) {
  const s = useUIStore.getState();
  const eff = ids.filter((id) => priorRating(s, id) !== rating);
  if (eff.length === 0) return;
  const entry: CullEntry = {
    label: `${rating === 0 ? 'Clear rating' : `${rating}★`}${countSuffix(eff.length)}`,
    redo: { kind: 'rating', groups: [{ ids: eff, value: rating }] },
    undo: { kind: 'rating', groups: groupByPrior(eff, (id) => priorRating(s, id)) },
  };
  commit(client, entry, 'Rating');
}
