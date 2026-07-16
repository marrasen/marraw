import { create } from 'zustand';
import { toast } from 'sonner';
import { setFlag, setRating, type FlagType } from '@/api/library';
import type { ApiClient } from '@/api/client';
import { useUIStore } from '@/stores/uiStore';

// Undo history for flag/rating mutations. A separate stack from the per-photo
// develop-edit history in editSession: culling actions are list-level and can
// touch many photos at once (a burst judgement, a multi-selection rating).
// Entries store inverse operations — the prior value per photo, grouped by
// distinct value so an undo replays with a minimal number of bulk RPCs. The
// server just re-sets current state (sidecars/XMP rebuild from the DB on
// every set), so no server-side support is needed.

export type CullOp =
  | { kind: 'flag'; groups: { ids: number[]; value: FlagType }[] }
  | { kind: 'rating'; groups: { ids: number[]; value: number }[] };

export interface CullEntry {
  label: string;
  undo: CullOp;
  redo: CullOp;
}

interface CullHistoryState {
  stack: CullEntry[];
  // Entries below index have been applied (undoable); entries at and above
  // it have been undone (redoable).
  index: number;
}

export const useCullHistory = create<CullHistoryState>(() => ({ stack: [], index: 0 }));

const CAP = 50;

// chPlay applies an op optimistically, then persists it: one bulk
// SetFlag/SetRating call per distinct-value group.
export function chPlay(client: ApiClient, op: CullOp): Promise<PromiseSettledResult<unknown>[]> {
  const { applyLocal } = useUIStore.getState();
  // Narrow the union before iterating: a group's `value` type follows the
  // op kind, which TS can't correlate through a ternary on an extracted `g`.
  if (op.kind === 'flag') {
    for (const g of op.groups) applyLocal(g.ids, { flag: g.value });
  } else {
    for (const g of op.groups) applyLocal(g.ids, { rating: g.value });
  }
  return Promise.allSettled(
    op.kind === 'flag'
      ? op.groups.map((g) => setFlag(client, g.ids, g.value))
      : op.groups.map((g) => setRating(client, g.ids, g.value)),
  );
}

export function chPush(entry: CullEntry) {
  useCullHistory.setState((s) => {
    // A new action truncates the redo tail (linear history), then caps.
    const stack = [...s.stack.slice(0, s.index), entry].slice(-CAP);
    return { stack, index: stack.length };
  });
}

// chRevert removes an entry whose persist failed — it must not sit in the
// history pretending it happened — and restores the priors best-effort (the
// caller already surfaced the persist error to the user).
export function chRevert(client: ApiClient, entry: CullEntry) {
  useCullHistory.setState((s) => {
    const i = s.stack.indexOf(entry);
    if (i < 0) return s;
    return {
      stack: [...s.stack.slice(0, i), ...s.stack.slice(i + 1)],
      index: s.index > i ? s.index - 1 : s.index,
    };
  });
  void chPlay(client, entry.undo);
}

export const chCanUndo = () => useCullHistory.getState().index > 0;
export const chCanRedo = () => {
  const s = useCullHistory.getState();
  return s.index < s.stack.length;
};

function reportFailure(results: PromiseSettledResult<unknown>[], what: string) {
  const bad = results.find((r): r is PromiseRejectedResult => r.status === 'rejected');
  if (bad) toast.error(`${what} failed: ${bad.reason instanceof Error ? bad.reason.message : bad.reason}`);
}

export function chUndo(client: ApiClient) {
  const s = useCullHistory.getState();
  if (s.index <= 0) return;
  const entry = s.stack[s.index - 1];
  useCullHistory.setState({ index: s.index - 1 });
  void chPlay(client, entry.undo).then((rs) => reportFailure(rs, 'Undo'));
  toast(`Undid: ${entry.label}`);
}

export function chRedo(client: ApiClient) {
  const s = useCullHistory.getState();
  if (s.index >= s.stack.length) return;
  const entry = s.stack[s.index];
  useCullHistory.setState({ index: s.index + 1 });
  void chPlay(client, entry.redo).then((rs) => reportFailure(rs, 'Redo'));
  toast(`Redid: ${entry.label}`);
}

// Prior values are meaningless across folders (setFolder wipes the optimistic
// overrides too), so the history dies with the folder.
useUIStore.subscribe((s, prev) => {
  if (s.folderId !== prev.folderId) useCullHistory.setState({ stack: [], index: 0 });
});
