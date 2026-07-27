// End-to-end check of the export presets settings surface against a running
// `marrawd --dev --port 8483`. Exercises: SetExportPresets round-trip through
// GetUISettings, server-side normalization (name clamp/trim, option fields
// falling back to the dialog defaults), the invalid-params rejections, and
// list replacement semantics. Restores the as-found preset list at the end.
//
//   node scripts/exportpresets-verify.mjs

const ws = new WebSocket('ws://127.0.0.1:8483/ws');
let nextId = 1;
const pending = new Map();

ws.onmessage = (ev) => {
  if (typeof ev.data !== 'string') return;
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
    }, 30_000);
  });
}

let failed = false;
const check = (name, ok, detail = '') => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
  if (!ok) failed = true;
};

await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error('ws connect failed — is `npm run dev` running?'));
});

const before = (await call('Settings.GetUISettings', [])).exportPresets ?? [];

try {
  // A raggedy preset: whitespace-padded name, out-of-range/unknown option
  // fields. The server must trim the name and normalize every option to the
  // dialog defaults (normalizeExportOptions).
  await call('Settings.SetExportPresets', [
    [
      {
        id: 'verify-1',
        name: '  Web JPEG  ',
        options: {
          format: 'png',
          jpegQuality: 500,
          resizeMode: 'bogus',
          edgePx: 4,
          colorSpace: 'adobergb',
          sharpenTarget: 'nope',
          sharpenAmount: 'high',
          fileNameTemplate: ' {name}-web ',
          exifMode: 'copyright',
          removeLocation: true,
          artist: ' Jane ',
          copyright: '',
          watermarkId: '',
        },
      },
    ],
  ]);
  const stored = (await call('Settings.GetUISettings', [])).exportPresets;
  check('one preset stored', stored.length === 1, `got ${stored.length}`);
  const p = stored[0] ?? { options: {} };
  check('name trimmed', p.name === 'Web JPEG', `got ${JSON.stringify(p.name)}`);
  check('valid fields survive', p.options.format === 'png' && p.options.colorSpace === 'adobergb' && p.options.sharpenAmount === 'high' && p.options.exifMode === 'copyright' && p.options.removeLocation === true);
  check('jpegQuality falls back', p.options.jpegQuality === 90, `got ${p.options.jpegQuality}`);
  check('resizeMode falls back', p.options.resizeMode === 'full', `got ${p.options.resizeMode}`);
  check('edgePx falls back', p.options.edgePx === 2160, `got ${p.options.edgePx}`);
  check('sharpenTarget falls back', p.options.sharpenTarget === 'off', `got ${p.options.sharpenTarget}`);
  check('template trimmed', p.options.fileNameTemplate === '{name}-web', `got ${JSON.stringify(p.options.fileNameTemplate)}`);
  check('artist trimmed', p.options.artist === 'Jane', `got ${JSON.stringify(p.options.artist)}`);

  // Rejections: empty id, empty name, and a whitespace-only name (the clamp
  // runs before the empty check).
  for (const [label, bad] of [
    ['empty id rejected', { id: '', name: 'x', options: {} }],
    ['empty name rejected', { id: 'verify-2', name: '', options: {} }],
    ['whitespace name rejected', { id: 'verify-2', name: '   ', options: {} }],
  ]) {
    let rejected = false;
    try {
      await call('Settings.SetExportPresets', [[bad]]);
    } catch {
      rejected = true;
    }
    check(label, rejected);
  }

  // A rejected write must not have clobbered the stored list.
  const after = (await call('Settings.GetUISettings', [])).exportPresets;
  check('rejected writes left the list intact', after.length === 1 && after[0].name === 'Web JPEG');

  // Whole-list replacement: an empty list clears.
  await call('Settings.SetExportPresets', [[]]);
  const cleared = (await call('Settings.GetUISettings', [])).exportPresets;
  check('empty list clears', cleared.length === 0, `got ${cleared.length}`);
} finally {
  await call('Settings.SetExportPresets', [before]);
  ws.close();
}

process.exit(failed ? 1 : 0);
