// One-off e2e probe for Edits.AutoAdjust: computes auto tone/wb/color for a
// spread of photos and sanity-checks the returned params (ranges, section
// isolation, idempotence after the exposure lands, timing).
// Usage: node scripts/auto-verify.mjs "<raw folder>"
const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/auto-verify.mjs "<raw folder>"');
  process.exit(1);
}
const PORT = process.env.MARRAW_PORT ?? 8483;
const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws`);
ws.binaryType = 'arraybuffer';

const pending = new Map();
let nextId = 1;
ws.onmessage = (ev) => {
  if (ev.data instanceof ArrayBuffer) return;
  const msg = JSON.parse(ev.data);
  if (msg.type === 'response') {
    pending.get(msg.id)?.resolve(msg.result);
    pending.delete(msg.id);
  } else if (msg.type === 'error') {
    pending.get(msg.id)?.reject(new Error(`${msg.code}: ${msg.message}`));
    pending.delete(msg.id);
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

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const fmt = (p) =>
  `ev=${p.expEV.toFixed(2)} con=${p.contrast} wh=${p.whites} bl=${p.blacks} ` +
  `ts=${p.toneShadows} th=${p.toneHighlights} vib=${p.vibrance} sat=${p.saturation} wb=${p.wbMode || 'camera'}`;

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed'));
});

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
const picks = [0, Math.floor(photos.length / 3), Math.floor((2 * photos.length) / 3), photos.length - 1]
  .map((i) => photos[i]);

for (const p of picks) {
  const base = (await call('Edits.GetEditParams', [p.id])) ?? {};
  const t = Date.now();
  const auto = await call('Edits.AutoAdjust', [p.id, base, ['all']]);
  const ms = Date.now() - t;
  console.log(`photo ${p.id} (${p.fileName}) [${ms}ms]\n    base ev=${(base.expEV ?? 0).toFixed(2)}\n    auto ${fmt(auto)}`);
  // ±5 EV is the validator's exposure range (edit.MinExpEV/MaxExpEV); auto
  // may legitimately return past LibRaw's ±3 exp_shift range, since the
  // residual is folded into the metered frame (api.Edits.statsDecode).
  const inRange =
    auto.expEV >= -5 && auto.expEV <= 5 &&
    [auto.contrast, auto.whites, auto.blacks, auto.toneShadows, auto.toneHighlights, auto.vibrance, auto.saturation]
      .every((v) => v >= -1 && v <= 1);
  check(inRange, 'all values within validator ranges');
  check(auto.wbMode === 'auto', 'wb section set wbMode auto');
  // autoEVLimit caps one pass at exactly 1.5 EV, so compare with an epsilon:
  // a bare `<= 1.5` fails on float noise (2.78 - 1.28 === 1.5000000000000002).
  const move = Math.abs(auto.expEV - (base.expEV ?? 0));
  check(move <= 1.5 + 1e-9, `exposure move bounded to ±1.5 EV (${move.toFixed(2)})`);

  // Section isolation: tone-only must not touch WB or color.
  const toneOnly = await call('Edits.AutoAdjust', [p.id, base, ['tone']]);
  check(
    (toneOnly.wbMode || '') === (base.wbMode || '') && toneOnly.vibrance === (base.vibrance ?? 0),
    'tone-only leaves wb/color untouched',
  );

  // Convergence, not idempotence: one pass moves at most ±1.5 EV, so a very
  // dark or very bright scene legitimately needs several to reach the target
  // (these hangar interiors want >3 EV). Re-run until the move dies out and
  // assert it settles quickly — a pass that hit the clamp has NOT landed yet,
  // so asserting no drift after a single pass is wrong.
  let cur = auto;
  let passes = 0;
  let drift = 0;
  do {
    const next = await call('Edits.AutoAdjust', [p.id, cur, ['all']]);
    drift = Math.abs(next.expEV - cur.expEV);
    cur = next;
    passes++;
  } while (drift > 0.35 && passes < 4);
  check(drift <= 0.35, `exposure converged in ${passes} extra pass(es), final drift ${drift.toFixed(2)} EV`);
}

// Validation: unknown sections must be rejected.
const rejected = await call('Edits.AutoAdjust', [picks[0].id, {}, ['bogus']]).then(() => false, () => true);
check(rejected, 'unknown section rejected');
const rejectedEmpty = await call('Edits.AutoAdjust', [picks[0].id, {}, []]).then(() => false, () => true);
check(rejectedEmpty, 'empty sections rejected');

console.log(failures ? `${failures} FAILURES` : 'ALL CHECKS PASSED');
ws.close();
process.exit(failures ? 1 : 0);
