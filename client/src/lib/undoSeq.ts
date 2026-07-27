// Shared monotonic counter ordering the two undo stacks (cullHistory's
// flag/rating entries and editSession's per-photo snapshots) against each
// other: Ctrl+Z outside Develop undoes whichever stack's top entry is newer.
// Gaps are fine — only relative order matters — and neither stack is
// persisted, so resetting with the page is correct.
let seq = 0;

export const nextSeq = () => ++seq;
