// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ApiClient } from '@/api/client';

type Call = { id: number; signal: AbortSignal; fail: (e: Error) => void };
let calls: Call[] = [];
const openInEditor = vi.fn();

vi.mock('@/api/edits', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/edits')>()),
  openInEditor: (...args: unknown[]) => openInEditor(...args),
}));

let hold: typeof import('@/lib/editorHold');
const client = {} as ApiClient;

beforeEach(async () => {
  vi.useFakeTimers();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  calls = [];
  openInEditor.mockReset();
  openInEditor.mockImplementation(
    (_c: unknown, id: number, opts: { signal: AbortSignal }) =>
      new Promise<void>((_resolve, reject) => {
        calls.push({ id, signal: opts.signal, fail: reject });
      }),
  );
  vi.resetModules();
  hold = await import('@/lib/editorHold');
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('holding the editor photo', () => {
  it('holds only after the user stays, so skimming starts none', async () => {
    for (const id of [1, 2, 3]) {
      const stop = hold.holdPhoto(client, id);
      await vi.advanceTimersByTimeAsync(100);
      stop();
    }
    const stop = hold.holdPhoto(client, 4);
    await vi.advanceTimersByTimeAsync(400);
    expect(calls.map((c) => c.id)).toEqual([4]);
    expect(hold.heldPhotoId()).toBe(4);
    stop();
    expect(calls[0].signal.aborted).toBe(true);
    expect(hold.heldPhotoId()).toBe(null);
  });

  it('holds again after a hold ends with an error, sooner first', async () => {
    const stop = hold.holdPhoto(client, 7);
    await vi.advanceTimersByTimeAsync(400);
    calls[0].fail(new Error('connection lost'));
    await vi.advanceTimersByTimeAsync(999);
    expect(calls).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(calls).toHaveLength(2);
    for (const wait of [5000, 5000, 30_000, 30_000]) {
      calls.at(-1)!.fail(new Error('still down'));
      await vi.advanceTimersByTimeAsync(wait);
    }
    expect(calls).toHaveLength(6);
    stop();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(calls).toHaveLength(6);
  });
});
