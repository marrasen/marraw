// @vitest-environment jsdom
//
// Characterisation tests for the preview render scheduler inside
// editSession.ts. Written against the behaviour as it stands, so the same
// suite can be run before and after moving the scheduler into a file of its
// own — the point is not that these rules are ideal, it is that they do not
// change while the code moves.
//
// The scheduler is the app's most timing-sensitive code and had no coverage:
// it coalesces renders rather than debouncing them, keeps two pending slots so
// a low frame and a sharp settle can both be owed, aborts in-flight full
// renders that a newer edit has made stale, and refires the queue from a
// finally block that also runs on abort. Every one of those is a rule a
// screenshot cannot see.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ApiClient } from '@/api/client';
import type { Params } from '@/api/edit';

const previewEdit = vi.fn();

vi.mock('@/api/edits', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/edits')>()),
  previewEdit: (...args: unknown[]) => previewEdit(...args),
}));

type Render = {
  px: number;
  params: Params;
  signal: AbortSignal;
  resolve: () => void;
  reject: (e?: unknown) => void;
};

let renders: Render[] = [];
let es: typeof import('@/lib/editSession');
const client = {} as ApiClient;

/** Renders still awaiting a resolution, in the order they were requested. */
const live = () => renders.filter((r) => !r.signal.aborted);

const DRAFT_PX = 1024;
const FULL_PX = 2048;

function draftParams(over: Partial<Params> = {}): Params {
  return { expEV: 0, contrast: 0, ...over } as unknown as Params;
}

/** Let the microtask queue drain so awaited renders settle. */
const tick = () => new Promise((r) => setTimeout(r, 0));

beforeEach(async () => {
  renders = [];
  previewEdit.mockReset();
  previewEdit.mockImplementation(
    (_client: unknown, _id: number, params: Params, px: number, opts: { signal: AbortSignal }) =>
      new Promise<Blob>((resolve, reject) => {
        renders.push({
          px,
          params,
          signal: opts.signal,
          resolve: () => resolve(new Blob(['x'])),
          reject: (e) => reject(e ?? new Error('aborted')),
        });
      }),
  );
  // jsdom has no object URLs.
  URL.createObjectURL = vi.fn(() => 'blob:test');
  URL.revokeObjectURL = vi.fn();

  // A fresh module per test: the scheduler's state is module-level by design.
  vi.resetModules();
  es = await import('@/lib/editSession');
  es.useEditSession.setState({ photoId: 1, draft: draftParams(), rendering: 0 });
});

afterEach(() => {
  for (const r of renders) r.reject();
});

describe('coalescing', () => {
  it('renders immediately when nothing is in flight, rather than debouncing', async () => {
    es.esUpdate(client, { expEV: 0.1 });
    await tick();
    expect(renders).toHaveLength(1);
    expect(renders[0].px).toBe(DRAFT_PX);
  });

  it('remembers only the newest request while one is in flight', async () => {
    es.esUpdate(client, { expEV: 0.1 });
    await tick();
    es.esUpdate(client, { expEV: 0.2 });
    es.esUpdate(client, { expEV: 0.3 });
    es.esUpdate(client, { expEV: 0.4 });
    await tick();
    // Still just the first: the rest collapsed into one pending slot.
    expect(renders).toHaveLength(1);

    renders[0].resolve();
    await tick();
    // Exactly one follow-up, carrying the newest state — not a queue of four.
    expect(renders).toHaveLength(2);
    expect(renders[1].params.expEV).toBe(0.4);
  });
});

describe('a drag frame supersedes a sharp one', () => {
  it('aborts an in-flight full render, because its pixels are now stale', async () => {
    // A settle is in flight...
    es.useEditSession.setState({ hoverParams: draftParams({ expEV: 9 }) });
    es.esHoverEnd(client);
    await tick();
    const first = renders[0];
    expect(first.px).toBe(DRAFT_PX);
    first.resolve();
    await tick();
    const settle = renders.find((r) => r.px === FULL_PX);
    expect(settle).toBeDefined();
    expect(settle!.signal.aborted).toBe(false);

    // ...and the user moves a slider.
    es.esUpdate(client, { expEV: 0.5 });
    await tick();
    expect(settle!.signal.aborted).toBe(true);
  });

  it('replaces a queued settle rather than stacking behind it', async () => {
    es.esUpdate(client, { expEV: 0.1 });
    await tick();
    es.useEditSession.setState({ hoverParams: null });
    es.esUpdate(client, { expEV: 0.2 }); // queue a draft
    renders[0].resolve();
    await tick();
    // One follow-up, low-res: the drag frame owns the slot.
    expect(renders).toHaveLength(2);
    expect(renders[1].px).toBe(DRAFT_PX);
  });
});

describe('the two pending slots', () => {
  // A step or preset toggle asks for a drag frame AND a settle in one tick.
  // With a single sticky slot every held-key step would render at 2048.
  it('paints the instant low frame first and keeps the sharp one queued', async () => {
    es.esUpdate(client, { expEV: 0.1 });
    await tick();
    expect(renders).toHaveLength(1);

    es.useEditSession.setState({ hoverParams: draftParams({ expEV: 5 }) });
    es.esHoverEnd(client); // draft + settle in one tick, both queued
    renders[0].resolve();
    await tick();

    expect(renders[1].px).toBe(DRAFT_PX);
    renders[1].resolve();
    await tick();
    expect(renders[2].px).toBe(FULL_PX);
  });
});

describe('the settled flag', () => {
  // The code refires the queue BEFORE decrementing the render counter,
  // specifically so the counter never touches zero while another frame is
  // owed. Sampling after the handoff cannot see the difference — by then the
  // refire has already incremented again — so watch every value the store
  // publishes instead.
  it('never reports settled while another frame is still owed', async () => {
    const seen: number[] = [];
    const stop = es.useEditSession.subscribe((st) => seen.push(st.rendering));

    es.esUpdate(client, { expEV: 0.1 });
    await tick();
    es.useEditSession.setState({ hoverParams: draftParams({ expEV: 5 }) });
    es.esHoverEnd(client); // a low frame and a settle are now both owed

    renders[0].resolve();
    await tick();
    const duringHandoff = seen.slice();
    renders[1].resolve();
    await tick();
    renders[2]?.resolve();
    await tick();
    stop();

    // The window that matters starts when the counter first goes up: before
    // that, esFlushDraft publishes the draft while nothing is rendering yet,
    // and a zero there is honest. From then until the queue drains, zero
    // means "nothing rendering", which the loupe reads as settled.
    const started = duringHandoff.findIndex((n) => n > 0);
    expect(started).toBeGreaterThanOrEqual(0);
    expect(duringHandoff.slice(started)).not.toContain(0);
    // And it does settle once everything has landed.
    expect(es.esPreviewSettled()).toBe(true);
  });
});

describe('recovery', () => {
  // The finally block refires the queue even when the render it followed was
  // aborted: the abort-on-supersede path wants its replacement immediately.
  it('renders the queued frame after an abort, not only after a success', async () => {
    es.useEditSession.setState({ hoverParams: draftParams({ expEV: 9 }) });
    es.esHoverEnd(client);
    await tick();
    renders[0].resolve();
    await tick();
    const settle = renders.find((r) => r.px === FULL_PX)!;

    es.esUpdate(client, { expEV: 0.5 }); // aborts the settle, queues a draft
    settle.reject();
    await tick();

    expect(live().some((r) => r.px === DRAFT_PX && r.params.expEV === 0.5)).toBe(true);
  });
});
