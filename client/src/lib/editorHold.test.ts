// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ConnectionError, type ApiClient, type ConnectionState } from '@/api/client';

type Call = { id: number; signal: AbortSignal; fail: (e: Error) => void };
let calls: Call[] = [];
const openInEditor = vi.fn();

vi.mock('@/api/edits', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/edits')>()),
  openInEditor: (...args: unknown[]) => openInEditor(...args),
}));

let hold: typeof import('@/lib/editorHold');

// A client whose connection state the test drives.
let state: ConnectionState = 'connected';
let listeners: ((s: ConnectionState) => void)[] = [];
const client = {
  isConnected: () => state === 'connected',
  onStateChange: (l: (s: ConnectionState) => void) => {
    listeners.push(l);
    return () => {
      listeners = listeners.filter((x) => x !== l);
    };
  },
} as unknown as ApiClient;
const setConnection = (s: ConnectionState) => {
  state = s;
  for (const l of [...listeners]) l(s);
};

beforeEach(async () => {
  vi.useFakeTimers();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  calls = [];
  state = 'connected';
  listeners = [];
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
  it('holds only after the user stays a second, so stepping starts none', async () => {
    for (const id of [1, 2, 3]) {
      const stop = hold.holdPhoto(client, id);
      await vi.advanceTimersByTimeAsync(700);
      stop();
    }
    const stop = hold.holdPhoto(client, 4);
    await vi.advanceTimersByTimeAsync(1000);
    expect(calls.map((c) => c.id)).toEqual([4]);
    stop();
    expect(calls[0].signal.aborted).toBe(true);
  });

  it('holds again as soon as the connection is back', async () => {
    const stop = hold.holdPhoto(client, 7);
    await vi.advanceTimersByTimeAsync(1000);
    state = 'reconnecting';
    calls[0].fail(new ConnectionError('network-error', 'connection lost'));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(calls).toHaveLength(1); // nothing while it is down
    setConnection('connected');
    await vi.advanceTimersByTimeAsync(0);
    expect(calls).toHaveLength(2);
    stop();
  });

  it('tries again after a backend error three times, then gives up', async () => {
    const stop = hold.holdPhoto(client, 8);
    await vi.advanceTimersByTimeAsync(1000);
    for (const wait of [5000, 30_000, 30_000]) {
      calls.at(-1)!.fail(new Error('photo not found'));
      await vi.advanceTimersByTimeAsync(wait - 1);
      const before = calls.length;
      await vi.advanceTimersByTimeAsync(1);
      expect(calls.length).toBe(before + 1);
    }
    calls.at(-1)!.fail(new Error('photo not found'));
    await vi.advanceTimersByTimeAsync(120_000);
    expect(calls).toHaveLength(4);
    stop();
  });

  it('stops trying once the photo is left', async () => {
    const stop = hold.holdPhoto(client, 9);
    await vi.advanceTimersByTimeAsync(1000);
    calls[0].fail(new Error('busy'));
    stop();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(calls).toHaveLength(1);
  });
});
