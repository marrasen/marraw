// @vitest-environment jsdom
//
// The loupe skips the skimming dwell before the 1:1 render of a state the
// user's own save just produced. The edit session records each save of its
// photo, at any selection size, with the hash the save returns, and forgets
// it on a photo switch.
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ApiClient } from '@/api/client';
import type { Params } from '@/api/edit';

const setEditParams = vi.fn(async () => ({ hash: 'h2', save: 1 }));
// Paste and reset answer with what each photo saved, in the order of ids.
const pasteEditParams = vi.fn(async (_c: unknown, ids: number[]) =>
  ids.map((id) => ({ hash: id === 1 ? 'h2' : `p${id}`, save: 1 })),
);
const resetEdits = vi.fn(async (_c: unknown, ids: number[]) =>
  ids.map((id) => ({ hash: id === 1 ? 'base' : `r${id}`, save: 1 })),
);

vi.mock('@/api/edits', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/edits')>()),
  setEditParams: (...args: unknown[]) => setEditParams(...(args as [])),
  pasteEditParams: (...args: unknown[]) => pasteEditParams(...(args as [unknown, number[]])),
  resetEdits: (...args: unknown[]) => resetEdits(...(args as [unknown, number[]])),
  previewEdit: () => new Promise(() => {}),
  previewDraft: () => new Promise(() => {}),
  previewExact: () => new Promise(() => {}),
  getEditParams: async () => null,
  generateFill: async () => ({}),
}));

let es: typeof import('@/lib/editSession');
const client = {} as ApiClient;
const draft = { expEV: 0, contrast: 0.2 } as unknown as Params;

beforeEach(async () => {
  URL.createObjectURL = vi.fn(() => 'blob:test');
  URL.revokeObjectURL = vi.fn();
  vi.resetModules();
  es = await import('@/lib/editSession');
  es.useEditSession.setState({ photoId: 1, applyIds: [1], draft, rendering: 0 });
  es.esShownHash(1, 'h1');
});

// Let the save promises answer.
const settle = () => new Promise((r) => setTimeout(r, 0));

describe('the committed state', () => {
  it('records a save of the photo with the hash it was made over', async () => {
    es.esCommit(client);
    expect(es.useEditSession.getState().committed).toMatchObject({ photoId: 1, sentFrom: 'h1', seq: 1, out: 1 });
    await settle();
    expect(es.useEditSession.getState().committed).toMatchObject({ out: 0, real: 'h2' });
  });

  it('records a save made with several photos selected', async () => {
    es.useEditSession.setState({ applyIds: [1, 2, 3] });
    es.esCommit(client);
    expect(pasteEditParams).toHaveBeenCalled();
    expect(es.useEditSession.getState().committed).toMatchObject({ photoId: 1, sentFrom: 'h1', out: 1 });
    await settle();
    const c = es.useEditSession.getState().committed;
    expect(es.savedHere(c, 1, 'h2')).toBe(true);
    expect(es.savedHere(c, 1, 'h1')).toBe(false);
  });

  it('is forgotten when the session moves to another photo', async () => {
    es.esCommit(client);
    await es.esLoad(client, 2, [2]);
    expect(es.useEditSession.getState().committed).toBeNull();
  });

  it('is forgotten once the saved state\'s tiles are warm', async () => {
    es.esCommit(client);
    await settle();
    es.esCommittedWarm(1, 'h1'); // the old state's warmth says nothing
    expect(es.useEditSession.getState().committed).not.toBeNull();
    es.esCommittedWarm(1, 'h2');
    expect(es.useEditSession.getState().committed).toBeNull();
  });
});

// A burst of saves (holding undo, stepping the history) skips the dwell
// only for the newest save's result, once no save is out.
describe('a burst of saves', () => {
  it('waits for the last save before its result counts', async () => {
    const answers: Array<(v: { hash: string; save: number }) => void> = [];
    setEditParams.mockImplementation(() => new Promise((r) => answers.push(r)));
    es.esCommit(client);
    es.esCommit(client, { contrast: 0.4 } as Partial<Params>);
    let c = es.useEditSession.getState().committed;
    expect(c).toMatchObject({ seq: 2, out: 2 });
    answers[0]({ hash: 'h2', save: 1 });
    await settle();
    c = es.useEditSession.getState().committed;
    // The first save's result shows, but the newer save is still out.
    expect(es.savedHere(c, 1, 'h2')).toBe(false);
    answers[1]({ hash: 'h3', save: 2 });
    await settle();
    c = es.useEditSession.getState().committed;
    expect(es.savedHere(c, 1, 'h2')).toBe(false); // not the newest save's result
    expect(es.savedHere(c, 1, 'h3')).toBe(true);
    setEditParams.mockImplementation(async () => ({ hash: 'h2', save: 1 }));
  });
});

describe('saves around a photo switch, no-op saves and resets', () => {
  // A save sent before the session left the photo must not answer into the
  // record of a save made after coming back.
  it('keeps an older save out of a newer record', async () => {
    const answers: Array<(v: { hash: string; save: number }) => void> = [];
    setEditParams.mockImplementation(() => new Promise((r) => answers.push(r)));
    es.esCommit(client);
    await es.esLoad(client, 2, [2]);
    await es.esLoad(client, 1, [1]);
    es.useEditSession.setState({ draft });
    es.esShownHash(1, 'h1');
    es.esCommit(client);
    answers[0]({ hash: 'h2', save: 1 });
    await settle();
    expect(es.useEditSession.getState().committed).toMatchObject({ out: 1 });
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h2')).toBe(false);
    answers[1]({ hash: 'h3', save: 2 });
    await settle();
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h3')).toBe(true);
    setEditParams.mockImplementation(async () => ({ hash: 'h2', save: 1 }));
  });

  // Crop entered and left with no change saves the same state again: the
  // real edit before it still skips the dwell.
  it('keeps the real edit after a save that changes nothing', async () => {
    es.esCommit(client);
    await settle();
    es.esShownHash(1, 'h2');
    es.esCommit(client); // answers h2 again: nothing changed
    await settle();
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h2')).toBe(true);
  });

  it('counts the user\'s own reset', async () => {
    es.esReset(client);
    expect(es.useEditSession.getState().committed).toMatchObject({ photoId: 1, sentFrom: 'h1', out: 1 });
    await settle();
    const c = es.useEditSession.getState().committed;
    expect(c).toMatchObject({ out: 0, real: 'base' });
    expect(es.savedHere(c, 1, 'base')).toBe(true);
  });

  // A reset back to the photo's original state: the hash it returns to is
  // the one before the first save, and still the user's own result.
  it('counts a reset back to the original hash', async () => {
    resetEdits.mockImplementationOnce(async (_c: unknown, ids: number[]) => ids.map(() => ({ hash: 'h1', save: 2 })));
    es.esCommit(client); // h1 → h2
    await settle();
    es.esShownHash(1, 'h2');
    es.esReset(client); // h2 → back to h1
    await settle();
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h1')).toBe(true);
  });

  // A paste answers with each photo's hash: the session photo's entry is
  // its result, and an earlier hash the loupe still shows is not.
  it('takes a paste\'s result from its answer', async () => {
    pasteEditParams.mockImplementationOnce(async (_c: unknown, ids: number[]) =>
      ids.map((id) => ({ hash: id === 1 ? 'h4' : 'p2', save: 3 })),
    );
    es.esCommit(client); // h1 → h2
    await settle();
    es.esShownHash(1, 'h2');
    es.useEditSession.setState({ applyIds: [2, 1] }); // the session photo second
    es.esCommit(client); // paste over h2 → h4
    await settle();
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h2')).toBe(false);
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h4')).toBe(true);
  });

  // A failed save changes nothing: the result before it, still on screen,
  // is still the user's own.
  it('keeps the earlier result after a failed save', async () => {
    es.esCommit(client); // h1 → h2
    await settle();
    es.esShownHash(1, 'h2');
    setEditParams.mockImplementationOnce(async () => {
      throw new Error('backend down');
    });
    es.esCommit(client, { contrast: 0.5 } as Partial<Params>);
    await settle();
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h2')).toBe(true);
  });

  // With several photos selected, a save that changes nothing (crop entered
  // and left) keeps the paste before it.
  it('keeps the earlier paste after a paste that changes nothing', async () => {
    es.useEditSession.setState({ applyIds: [1, 2] });
    es.esCommit(client); // paste h1 → h2
    await settle();
    es.esShownHash(1, 'h2');
    es.esCommit(client); // paste again: answers h2, nothing changed
    await settle();
    expect(es.savedHere(es.useEditSession.getState().committed, 1, 'h2')).toBe(true);
  });
});
