// One aprot WebSocket client for the verify scripts.
//
// Every probe in scripts/ used to hand-roll this: open the socket, keep a map
// of pending ids, match responses, collect pushes, poll TaskStateEvent. Forty
// or so copies, each with its own subset of the protocol's sharp edges — and
// because they are run by hand rather than in CI, a copy that stopped matching
// the wire failed as a hang or a silent wrong answer, months after the change
// that broke it.
//
// The sharp edges, all of which cost someone an afternoon at least once:
//
//   * Params are POSITIONAL. `params` is an array — `[folderId]`, not
//     `{folderId}`. An object comes back as "cannot unmarshal JSON object
//     into Go []jsontext.Value", which reads like a type error in the handler.
//
//   * Blob-returning methods reply as BINARY frames, not JSON. A handler that
//     only looks at string messages drops the reply and the call hangs
//     forever, looking exactly like a server deadlock. The frame is a 4-byte
//     big-endian header length, that many bytes of JSON header (carrying the
//     request `id` and `contentType`), then the payload — so the id comes off
//     the header rather than being guessed at. `call` resolves those as
//     {blob, contentType}.
//
//   * The server sends a `config` frame first, and pushes reach a connection
//     that has not authenticated yet. Filter for the frame you are asserting
//     on rather than assuming the first one is yours.
//
//   * A stale marrawd survives `pkill -f "npm run dev"` and shadows a rebuild,
//     binding 8483 before the new one can. `connect` says so rather than
//     timing out anonymously.

const DEFAULT_URL = 'ws://127.0.0.1:8483/ws';
const DEFAULT_TIMEOUT = 120_000;

/**
 * Opens a connection and returns the handful of things a probe needs.
 *
 * @returns {Promise<{
 *   call: (method: string, params?: unknown[], opts?: {timeoutMs?: number}) => Promise<any>,
 *   send: (method: string, params?: unknown[], opts?: {timeoutMs?: number}) => {id: string, promise: Promise<any>},
 *   cancel: (id: string) => void,
 *   waitTask: (taskId: string, opts?: {timeoutMs?: number}) => Promise<any>,
 *   waitPush: (event: string, match?: (data: any) => boolean, opts?: {timeoutMs?: number}) => Promise<any>,
 *   pushes: {event: string, data: any}[],
 *   close: () => void,
 * }>}
 */
export async function connect({ url = DEFAULT_URL, token, timeoutMs = DEFAULT_TIMEOUT } = {}) {
  const ws = new WebSocket(url);
  ws.binaryType = 'arraybuffer';
  const pending = new Map();
  const pushes = [];
  const pushWaiters = [];
  let nextId = 1;

  ws.onmessage = async (ev) => {
    if (typeof ev.data !== 'string') {
      const buf = ev.data instanceof ArrayBuffer ? ev.data : await ev.data.arrayBuffer();
      const headerLen = new DataView(buf).getUint32(0, false);
      const header = JSON.parse(new TextDecoder().decode(new Uint8Array(buf, 4, headerLen)));
      pending.get(header.id)?.resolve({
        blob: new Uint8Array(buf, 4 + headerLen),
        contentType: header.contentType,
      });
      pending.delete(header.id);
      return;
    }
    const msg = JSON.parse(ev.data);
    if (msg.type === 'response') {
      pending.get(msg.id)?.resolve(msg.result);
      pending.delete(msg.id);
    } else if (msg.type === 'error') {
      // Keep the numeric code on the error: callers assert on specific ones
      // (a cancel settles as -32800), and string-matching a message is the
      // kind of thing that rots the next time the wording changes.
      const err = new Error(`${msg.code}: ${msg.message}`);
      err.code = msg.code;
      pending.get(msg.id)?.reject(err);
      pending.delete(msg.id);
    } else if (msg.type === 'push') {
      const push = { event: msg.event, data: msg.data };
      pushes.push(push);
      for (const w of pushWaiters.slice()) {
        if (w.event === push.event && (!w.match || w.match(push.data))) {
          pushWaiters.splice(pushWaiters.indexOf(w), 1);
          w.resolve(push.data);
        }
      }
    }
  };

  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = () =>
      reject(
        new Error(
          `cannot reach ${url}. Is marrawd running? A stale one can hold 8483 ` +
            `and shadow a rebuild — check with: ss -ltnp | grep 8483`,
        ),
      );
  });

  if (token) {
    ws.send(JSON.stringify({ type: 'auth', token }));
  }

  // send exposes the request id, so a caller can cancel mid-flight — which is
  // the contract the immediate-settle scheduler leans on.
  function send(method, params = [], { timeoutMs: perCall = timeoutMs } = {}) {
    if (!Array.isArray(params)) {
      throw new TypeError(
        `${method}: params must be an ARRAY (positional). An object gives ` +
          `"cannot unmarshal JSON object into Go []jsontext.Value".`,
      );
    }
    const id = String(nextId++);
    const promise = new Promise((resolve, reject) => {
      pending.set(id, { id, resolve, reject });
      setTimeout(() => {
        if (pending.has(id)) {
          pending.delete(id);
          reject(new Error(`timeout: ${method}`));
        }
      }, perCall);
    });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
    return { id, promise };
  }

  const call = (method, params = [], opts) => send(method, params, opts).promise;
  const cancel = (id) => ws.send(JSON.stringify({ type: 'cancel', id }));

  function waitPush(event, match, { timeoutMs: wait = timeoutMs } = {}) {
    const already = pushes.find((p) => p.event === event && (!match || match(p.data)));
    if (already) return Promise.resolve(already.data);
    return new Promise((resolve, reject) => {
      const w = { event, match, resolve };
      pushWaiters.push(w);
      setTimeout(() => {
        const i = pushWaiters.indexOf(w);
        if (i >= 0) {
          pushWaiters.splice(i, 1);
          reject(new Error(`timeout waiting for push ${event}`));
        }
      }, wait);
    });
  }

  // The field is `status`, and its values are the TaskNodeStatus enum in
  // client/src/api/tasks-handler.ts — created / running / completed / failed.
  // Worth naming: guessing `state === 'done'` here would poll for the life of
  // the timeout and report a hang rather than a mismatch.
  async function waitTask(taskId, { timeoutMs: wait = timeoutMs } = {}) {
    const until = Date.now() + wait;
    while (Date.now() < until) {
      for (const p of pushes) {
        if (p.event !== 'TaskStateEvent') continue;
        const task = p.data.tasks?.find((t) => t.id === taskId);
        if (!task) continue;
        if (task.status === 'completed') return task;
        if (task.status === 'failed') throw new Error(`task ${taskId} failed: ${task.error ?? ''}`);
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error(`timeout waiting for task ${taskId}`);
  }

  return { call, send, cancel, waitTask, waitPush, pushes, close: () => ws.close() };
}

/** A tiny pass/fail tally, so probes report the same way. */
export function checker() {
  let failed = 0;
  const eq = (label, got, want) => {
    const ok = JSON.stringify(got) === JSON.stringify(want);
    if (!ok) failed++;
    console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  };
  const ok = (label, cond, detail = '') => {
    if (!cond) failed++;
    console.log(`${cond ? 'ok  ' : 'FAIL'} ${label}${detail ? ': ' + detail : ''}`);
  };
  return { eq, ok, done: () => { console.log(failed ? `\n${failed} FAILED` : '\nall ok'); process.exit(failed ? 1 : 0); } };
}
