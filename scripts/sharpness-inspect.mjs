// Pulls the lowest- and highest-scoring photos of a folder so a human (or a
// vision model) can judge whether the sharpness metric discriminates real
// softness. Saves 1024px renditions to build/sharpness/.
//   node scripts/sharpness-inspect.mjs "<raw folder>"
import { mkdirSync, writeFileSync } from 'node:fs';

const FOLDER = process.argv[2];
const ws = new WebSocket('ws://127.0.0.1:8483/ws');
const pending = new Map();
let id = 1;
ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
  const m = JSON.parse(ev.data);
  if (m.type === 'response') pending.get(m.id)?.(m.result);
};
const call = (method, params) => new Promise((res) => {
  const i = String(id++);
  pending.set(i, res);
  ws.send(JSON.stringify({ type: 'request', id: i, method, params }));
});
ws.onopen = async () => {
  const info = await call('Library.OpenFolder', [FOLDER]);
  const photos = (await call('Library.ListPhotos', [info.folderId])).filter((p) => p.sharpness != null);
  photos.sort((a, b) => a.sharpness - b.sharpness);
  const picks = [...photos.slice(0, 3), ...photos.slice(-2)];
  mkdirSync('build/sharpness', { recursive: true });
  for (const p of picks) {
    const r = await fetch(`http://127.0.0.1:8483/img/${p.id}/1024?v=${p.cacheKey}&r=r8${p.editHash && p.editHash !== 'base' ? `&e=${p.editHash}` : ''}`);
    const buf = Buffer.from(await r.arrayBuffer());
    const name = `score-${Math.round(p.sharpness)}-${p.fileName.replace(/\.\w+$/, '')}.jpg`;
    writeFileSync(`build/sharpness/${name}`, buf);
    console.log(name, buf.length, 'bytes');
  }
  process.exit(0);
};
