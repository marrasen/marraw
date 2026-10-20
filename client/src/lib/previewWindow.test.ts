// @vitest-environment jsdom
//
// The window scheduler's rules: one window renders at a time and the newest
// wanted one goes next; the same window is not asked for twice; a null
// answer takes the window down (and is asked once more when it was wanted
// again meanwhile); a window let go never comes back from a late answer.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ApiClient } from '@/api/client';
import type { Params } from '@/api/edit';

const previewWindow = vi.fn();

vi.mock('@/api/edits', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/edits')>()),
  previewWindow: (...args: unknown[]) => previewWindow(...args),
}));

type Call = { params: Params; rect: number[]; signal: AbortSignal; answer: (b: Blob | null) => void; fail: () => void };
let calls: Call[] = [];
let pw: typeof import('@/lib/previewWindow');
const client = {} as ApiClient;
const tick = () => new Promise((r) => setTimeout(r, 0));

function ask(contrast: number, rect: [number, number, number, number] = [0, 0, 100, 100]) {
  return { photoId: 1, params: { contrast } as unknown as Params, bust: 0, rect, dims: [4000, 3000] as [number, number] };
}

beforeEach(async () => {
  calls = [];
  previewWindow.mockImplementation(
    (_c: unknown, _id: number, params: Params, x: number, y: number, w: number, h: number, opts: { signal: AbortSignal }) =>
      new Promise<Blob | null>((resolve, reject) => {
        calls.push({ params, rect: [x, y, w, h], signal: opts.signal, answer: resolve, fail: () => reject(new Error('x')) });
      }),
  );
  globalThis.URL.createObjectURL = vi.fn(() => 'blob:x');
  globalThis.URL.revokeObjectURL = vi.fn();
  pw = await import('@/lib/previewWindow');
  pw.resetWindowForTest();
});

afterEach(() => vi.clearAllMocks());

describe('previewWindow', () => {
  it('renders one at a time, the newest wanted next', async () => {
    pw.wantWindow(client, ask(0.1));
    pw.wantWindow(client, ask(0.2));
    pw.wantWindow(client, ask(0.3));
    expect(calls).toHaveLength(1);
    calls[0].answer(new Blob(['a']));
    await tick();
    expect(calls).toHaveLength(2);
    expect((calls[1].params as unknown as { contrast: number }).contrast).toBe(0.3);
    expect(pw.useLoupeWindow.getState().win?.photoId).toBe(1);
  });

  it('does not ask for the window showing or rendering again', async () => {
    pw.wantWindow(client, ask(0.1));
    pw.wantWindow(client, ask(0.1));
    expect(calls).toHaveLength(1);
    calls[0].answer(new Blob(['a']));
    await tick();
    pw.wantWindow(client, ask(0.1));
    expect(calls).toHaveLength(1);
  });

  it('takes the window down on a null answer, and asks again when wanted again meanwhile', async () => {
    pw.wantWindow(client, ask(0.1));
    calls[0].answer(new Blob(['a']));
    await tick();
    pw.wantWindow(client, ask(0.2));
    pw.wantWindow(client, ask(0.2)); // the save asking for the drag's last frame
    calls[1].answer(null);
    await tick();
    expect(pw.useLoupeWindow.getState().win).toBeNull();
    expect(calls).toHaveLength(3); // asked once more
    calls[2].answer(null);
    await tick();
    expect(calls).toHaveLength(3); // and only once
  });

  it('going back to the window showing lets the other one rendering go', async () => {
    pw.wantWindow(client, ask(0.1));
    calls[0].answer(new Blob(['a']));
    await tick();
    pw.wantWindow(client, ask(0.2)); // a preset hovered
    pw.wantWindow(client, ask(0.1)); // and left
    expect(calls[1].signal.aborted).toBe(true);
    calls[1].answer(new Blob(['b']));
    await tick();
    expect(pw.useLoupeWindow.getState().win?.paramsKey).toBe(JSON.stringify({ contrast: 0.1 }));
  });

  it('lets a window of a view no longer shown go, but not a drag frame', async () => {
    pw.wantWindow(client, ask(0.1, [0, 0, 100, 100]));
    pw.wantWindow(client, ask(0.1, [50, 0, 100, 100])); // panned
    expect(calls[0].signal.aborted).toBe(true);
    expect(calls).toHaveLength(2);
    pw.wantWindow(client, ask(0.2, [50, 0, 100, 100])); // a drag frame
    expect(calls[1].signal.aborted).toBe(false);
  });

  it('counts an edit on screen only a moment as a drag frame', () => {
    const now = vi.spyOn(performance, 'now');
    now.mockReturnValue(1000);
    pw.noteShownEdit('a', false);
    now.mockReturnValue(1100);
    pw.noteShownEdit('b', false); // 'a' lasted 100 ms
    expect(pw.useLoupeWindow.getState().lastWasDrag).toBe(true);
    now.mockReturnValue(5000);
    pw.noteShownEdit('c', false); // 'b' had settled
    expect(pw.useLoupeWindow.getState().lastWasDrag).toBe(false);
    // Into and out of a hovered preset, however quick, is no drag.
    now.mockReturnValue(5100);
    pw.noteShownEdit('hover', true);
    expect(pw.useLoupeWindow.getState().lastWasDrag).toBe(false);
    now.mockReturnValue(5200);
    pw.noteShownEdit('c', false);
    expect(pw.useLoupeWindow.getState().lastWasDrag).toBe(false);
    now.mockRestore();
  });

  it('asks again when the fills or maps of the photo change under the same params', async () => {
    pw.wantWindow(client, ask(0.1));
    calls[0].answer(new Blob(['a']));
    await tick();
    pw.wantWindow(client, { ...ask(0.1), bust: 1 });
    expect(calls).toHaveLength(2);
  });

  it('drops a late answer for a window let go', async () => {
    pw.wantWindow(client, ask(0.1));
    pw.dropWindow();
    expect(calls[0].signal.aborted).toBe(true);
    calls[0].answer(new Blob(['a']));
    await tick();
    expect(pw.useLoupeWindow.getState().win).toBeNull();
  });

  it('asks for no window of a view too large', () => {
    pw.wantWindow(client, ask(0.1, [0, 0, 4000, 3000]));
    expect(calls).toHaveLength(0);
  });

  it('a failed window takes the window down without an error', async () => {
    pw.wantWindow(client, ask(0.1));
    calls[0].fail();
    await tick();
    expect(pw.useLoupeWindow.getState().win).toBeNull();
  });
});
