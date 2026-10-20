// @vitest-environment jsdom
//
// jsdom: lib/backend.ts reads window.location at module scope, and the hook
// renders through react-dom.
import { act, createElement, useEffect } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Photo } from '@/api/library';
import { useTilesWarm } from '@/lib/tiles';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const photo = { id: 1, cacheKey: 'abcdef', editHash: 'h1' } as unknown as Photo;
let renders: string[] = [];
let root: Root;
let host: HTMLDivElement;

function Probe(props: { want: boolean; immediate: boolean }) {
  useTilesWarm(photo, props.want, true, props.immediate);
  return null;
}

const show = (want: boolean, immediate: boolean) =>
  act(async () => {
    root.render(createElement(Probe, { want, immediate }));
  });

beforeEach(() => {
  vi.useFakeTimers();
  renders = [];
  globalThis.fetch = vi.fn(async (url: string | URL | Request) => {
    const u = String(url);
    if (u.includes('cacheOnly=1')) return new Response(null, { status: 404 });
    renders.push(u);
    return new Response(null, { status: 200 });
  }) as typeof fetch;
  host = document.createElement('div');
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  vi.useRealTimers();
});

describe('kicking the 1:1 render', () => {
  it('waits the skimming dwell before rendering a missing tile set', async () => {
    await show(true, false);
    await act(() => vi.advanceTimersByTimeAsync(300));
    expect(renders).toHaveLength(0);
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(renders).toHaveLength(1);
  });

  // The editor's own save: tiles are wanted only once its settle has landed
  // and the preview gives way, well after the new hash arrived. The render
  // must still start at once then.
  // A short dwell stays, so a burst of saves coalesces into one render.
  it('renders soon, when tiles come to be wanted, for the editor just saved', async () => {
    await show(false, true);
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(renders).toHaveLength(0);
    await show(true, true);
    await act(() => vi.advanceTimersByTimeAsync(100));
    expect(renders).toHaveLength(0);
    await act(() => vi.advanceTimersByTimeAsync(30));
    expect(renders).toHaveLength(1);
  });

  // The save's new hash can reach the loupe after tiles came to be wanted
  // (the patch, then the answer): the dwell under way is cut short.
  it('cuts a dwell under way short when the save turns up', async () => {
    await show(true, false);
    await act(() => vi.advanceTimersByTimeAsync(130));
    expect(renders).toHaveLength(0);
    await show(true, true);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(renders).toHaveLength(1); // 120 ms had passed already
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(renders).toHaveLength(1); // the cut-short timer does not fire again
  });
});

// The warmth answers for the state it was found for: the next state's hash
// arriving reads cold at once, before any probe of it has run.
describe('warmth', () => {
  it('is for the tile set it was found for', async () => {
    const seen: boolean[] = [];
    function W(props: { p: Photo; report: (warm: boolean) => void }) {
      props.report(useTilesWarm(props.p, true, true, false));
      return null;
    }
    const report = (warm: boolean) => void seen.push(warm);
    globalThis.fetch = vi.fn(async () => new Response(null, { status: 200 })) as typeof fetch;
    await act(async () => root.render(createElement(W, { p: photo, report })));
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(seen.at(-1)).toBe(true);
    globalThis.fetch = vi.fn(() => new Promise<Response>(() => {})) as typeof fetch;
    const next = { ...photo, editHash: 'h2' } as unknown as Photo;
    const from = seen.length;
    await act(async () => root.render(createElement(W, { p: next, report })));
    // Not even the render the new hash arrives in reads the old warmth.
    expect(seen.slice(from)).not.toContain(true);
  });
});

// A tile set found warm once can be evicted since (the janitor): the next
// probe's miss must make it read cold again, not keep the old warmth.
describe('warmth', () => {
  const seen = { warm: false };
  function WarmProbe(props: { want: boolean }) {
    const warm = useTilesWarm(photo, props.want, false);
    useEffect(() => {
      seen.warm = warm;
    });
    return null;
  }
  const showWarm = (want: boolean) =>
    act(async () => {
      root.render(createElement(WarmProbe, { want }));
    });

  it('reads cold once a warm set is found gone', async () => {
    let cached = true;
    globalThis.fetch = vi.fn(async () => new Response(null, { status: cached ? 200 : 404 })) as typeof fetch;
    await showWarm(true);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(seen.warm).toBe(true);
    await showWarm(false);
    cached = false;
    await showWarm(true);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(seen.warm).toBe(false);
  });
});
