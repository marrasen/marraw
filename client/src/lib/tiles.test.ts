// @vitest-environment jsdom
//
// jsdom: lib/backend.ts reads window.location at module scope, and the hook
// renders through react-dom.
import { act, createElement } from 'react';
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
  it('renders at once, when tiles come to be wanted, for the editor just saved', async () => {
    await show(false, true);
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(renders).toHaveLength(0);
    await show(true, true);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(renders).toHaveLength(1);
  });
});
