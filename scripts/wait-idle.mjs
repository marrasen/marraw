// Opens a folder and blocks until every background task (scan, calibrate,
// pre-render) has finished — the "fully pre-rendered, untouched folder"
// starting state. Usage: node scripts/wait-idle.mjs "<raw folder>"
const FOLDER = process.argv[2];
const ws = new WebSocket('ws://127.0.0.1:8483/ws');
const pending = new Map();
let id = 1;
let lastTasks = [];
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const m = JSON.parse(ev.data);
  if (m.type === 'response') pending.get(m.id)?.(m.result);
  else if (m.type === 'push' && m.event === 'TaskStateEvent') lastTasks = m.data.tasks ?? [];
  else if (m.type === 'push' && m.event === 'TaskUpdateEvent') {
    const t = lastTasks.find((x) => x.id === m.data.id);
    if (t) Object.assign(t, m.data);
  }
};
const call = (method, params) => new Promise((res) => {
  const i = String(id++);
  pending.set(i, res);
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
});
ws.onopen = async () => {
  const t0 = Date.now();
  const info = await call('Library.OpenFolder', [FOLDER]);
  console.log(`opened: ${info.photoCount} photos; waiting for background passes…`);
  let quietSince = Date.now();
  for (;;) {
    const active = lastTasks.filter((t) => t.status === 'running' || t.status === 'pending');
    if (active.length > 0) {
      quietSince = Date.now();
      process.stdout.write(`\r${active.map((t) => `${t.title} ${t.current ?? 0}/${t.total ?? 0}`).join(' | ')}        `);
    } else if (Date.now() - quietSince > 8000) {
      break; // 8s with no active tasks = passes done
    }
    if (Date.now() - t0 > 20 * 60_000) { console.log('\ntimeout'); process.exit(1); }
    await new Promise((s) => setTimeout(s, 1000));
  }
  console.log(`\nidle after ${Math.round((Date.now() - t0) / 1000)}s`);
  process.exit(0);
};
