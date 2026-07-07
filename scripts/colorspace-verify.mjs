// Wire probe: exporting with colorSpace=adobergb embeds an ICC profile in
// the JPEG; srgb stays untagged. Needs marrawd --dev :8483 and a RAW folder.
//
//   node scripts/colorspace-verify.mjs "D:\Photos\<raw folder>"

import { mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/colorspace-verify.mjs <raw-folder>');
  process.exit(1);
}

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();
const taskEvents = [];
ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
  } else if (msg.type === 'push') {
    taskEvents.push(msg);
  }
};
function call(method, params) {
  const id = String(nextId++);
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ type: 'request', id, method, params }));
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id);
        reject(new Error(`timeout: ${method}`));
      }
    }, 120_000);
  });
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('cannot connect to marrawd :8483'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
if (photos.length === 0) throw new Error('no photos');
const id = photos[0].id;

const results = {};
const check = (name, ok, detail = '') => {
  results[name] = ok;
  console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` (${detail})` : ''}`);
};

const out = mkdtempSync(join(tmpdir(), 'marraw-cs-'));
try {
  for (const space of ['srgb', 'adobergb']) {
    const destDir = join(out, space);
    await call('Export.StartExport', [
      {
        photoIds: [id],
        destDir,
        format: 'jpeg',
        jpegQuality: 85,
        longEdge: 1024,
        colorSpace: space,
        createDir: true,
      },
    ]);
    // Poll for the output file (single small photo, exports fast).
    let file = null;
    for (let i = 0; i < 120 && !file; i++) {
      await sleep(500);
      try {
        const files = readdirSync(destDir).filter((f) => f.toLowerCase().endsWith('.jpg'));
        if (files.length > 0) file = join(destDir, files[0]);
      } catch {
        // dest not created yet
      }
    }
    if (!file) {
      check(`${space}Export`, false, 'no output file');
      continue;
    }
    await sleep(500); // let the atomic rename settle
    const bytes = readFileSync(file);
    const hasICC = bytes.includes(Buffer.from('ICC_PROFILE\x00'));
    const hasDesc = bytes.includes(Buffer.from('Adobe RGB (1998)'));
    if (space === 'srgb') {
      check('srgbUntagged', !hasICC);
    } else {
      check('adobergbTagged', hasICC && hasDesc, `${bytes.length} bytes`);
    }
  }
} finally {
  ws.close();
  for (let i = 0; i < 20; i++) {
    try {
      rmSync(out, { recursive: true, force: true });
      break;
    } catch {
      await sleep(500);
    }
  }
}

const failed = Object.values(results).filter((v) => !v).length;
console.log(failed ? `${failed} COLORSPACE CHECKS FAILED` : 'ALL COLORSPACE CHECKS PASSED');
process.exit(failed ? 1 : 0);
