// End-to-end check of the B&W treatment against a running
// `marrawd --dev --port 8483`. Exercises the whole render path by exporting
// real JPEGs and measuring them (see bw-verify.py), plus the param
// round-trip, the XMP mapping, and the one thing the after-the-masks stage
// order buys us: local adjustments still select on color under B&W.
//
//   node scripts/bw-verify.mjs "/path/to/disposable-raw-folder"
//
// Point it at a DISPOSABLE copy of a shoot — editing writes .marraw.json
// sidecars next to the RAWs, and the rawXmp pass writes .xmp files.

import { connect } from './lib/rpc.mjs';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const FOLDER = process.argv[2];
if (!FOLDER) {
  console.error('usage: node scripts/bw-verify.mjs <disposable-raw-folder>');
  process.exit(1);
}
const HERE = dirname(fileURLToPath(import.meta.url));

const { call, waitTask, close } = await connect();

let failures = 0;
const check = (cond, name) => {
  console.log(`  ${cond ? 'PASS' : 'FAIL'}  ${name}`);
  if (!cond) failures++;
};
const step = (name) => console.log(name);

const info = await call('Library.OpenFolder', [FOLDER]);
const photos = await call('Library.ListPhotos', [info.folderId]);
step(`OpenFolder -> ${photos.length} photos`);
if (!photos.length) throw new Error('no RAWs in the fixture');
const p = photos[0];

// Renders one params set to a JPEG and returns PIL's measurements of it:
// mean chroma (max-min per pixel), the mean per-channel values, and the mean
// luma. Exporting rather than poking pixels directly is the point — it runs
// the same ApplyFinish every other render path uses.
const dirs = [];
async function measure(label, params) {
  const dest = mkdtempSync(join(tmpdir(), 'marraw-bw-'));
  dirs.push(dest);
  await call('Edits.SetEditParams', [p.id, params]);
  const ref = await call('Export.StartExport', [
    {
      photoIds: [p.id], destDir: dest, format: 'jpeg', jpegQuality: 92,
      longEdge: 800, colorSpace: 'srgb', sharpenTarget: 'off',
      sharpenAmount: 'standard', fileNameTemplate: '', exifMode: 'all',
      removeLocation: false, artist: '', copyright: '', watermarkId: '',
      createDir: false,
    },
  ]);
  const task = await waitTask(ref.taskId);
  if (task.status !== 'completed') throw new Error(`${label}: export ${task.status}`);
  const file = join(dest, readdirSync(dest).find((f) => /\.jpe?g$/i.test(f)));
  const out = JSON.parse(execFileSync('python3', [join(HERE, 'bw-verify.py'), file], { encoding: 'utf8' }));
  step(`  ${label}: chroma ${out.chroma.toFixed(1)} luma ${out.luma.toFixed(1)} rgb ${out.r.toFixed(1)}/${out.g.toFixed(1)}/${out.b.toFixed(1)}`);
  return out;
}

// --- 1. The conversion itself. ---
step('render:');
const color = await measure('color', { contrast: 0.1 });
const bw = await measure('bw', { contrast: 0.1, bw: true });
check(color.chroma > 5, `color render has chroma (${color.chroma.toFixed(1)})`);
check(bw.chroma < 0.6, `bw render is neutral (chroma ${bw.chroma.toFixed(1)})`);

// --- 2. The colored filter: a band must move its own hue's gray. ---
const lum = (i, v) => { const a = Array(8).fill(0); a[i] = v; return a; };
const darkRed = await measure('bw red band -1', { contrast: 0.1, bw: true, hslLum: lum(0, -1) });
const liteRed = await measure('bw red band +1', { contrast: 0.1, bw: true, hslLum: lum(0, 1) });
check(darkRed.chroma < 0.6 && liteRed.chroma < 0.6, 'filtered renders stay neutral');
check(darkRed.luma < bw.luma, `red -1 darkens (${darkRed.luma.toFixed(1)} < ${bw.luma.toFixed(1)})`);
check(liteRed.luma > bw.luma, `red +1 lightens (${liteRed.luma.toFixed(1)} > ${bw.luma.toFixed(1)})`);

// --- 3. Sepia: split toning tints the gray, warm means R > G > B. ---
const sepia = await measure('sepia', {
  contrast: 0.1, bw: true,
  splitShadowHue: 40, splitShadowAmt: 0.8, splitHighlightHue: 40, splitHighlightAmt: 0.8,
});
check(sepia.r > sepia.g && sepia.g > sepia.b, `sepia is warm (R>G>B: ${sepia.r.toFixed(1)}/${sepia.g.toFixed(1)}/${sepia.b.toFixed(1)})`);

// --- 4. Param round-trip: bw survives a save, and false leaves no trace. ---
await call('Edits.SetEditParams', [p.id, { contrast: 0.1, bw: true }]);
let stored = await call('Edits.GetEditParams', [p.id]);
check(stored.bw === true, 'bw:true round-trips through the store');
await call('Edits.SetEditParams', [p.id, { contrast: 0.1, bw: false }]);
stored = await call('Edits.GetEditParams', [p.id]);
check(!stored.bw, `bw:false reads back as color (${JSON.stringify(stored.bw)})`);

// --- 5. Local adjustments still see color under B&W. This is why the
// conversion runs after the masks: a hue-range mask that selected a red
// jacket must still select it once the frame renders gray. ---
const at = [0.5, 0.5];
const withRangeMask = (extra) => ({
  contrast: 0.1, ...extra,
  masks: [{ type: 'range', rangeLumaLo: 0, rangeLumaHi: 1 }],
});
const pickColor = await call('Edits.PickRangeColor', [p.id, withRangeMask({}), ...at, 0]);
const pickBW = await call('Edits.PickRangeColor', [p.id, withRangeMask({ bw: true }), ...at, 0]);
const win = (q) => [q.masks?.[0]?.rangeHueLo, q.masks?.[0]?.rangeHueHi];
check(
  JSON.stringify(win(pickColor)) === JSON.stringify(win(pickBW)),
  `range-mask hue pick is unchanged by B&W (${JSON.stringify(win(pickBW))})`,
);

// --- 6. XMP carries the conversion into Lightroom. ---
const xmpDest = mkdtempSync(join(tmpdir(), 'marraw-bw-xmp-'));
dirs.push(xmpDest);
await call('Edits.SetEditParams', [p.id, { contrast: 0.1, bw: true, hslLum: lum(0, -0.6) }]);
const xref = await call('Export.StartExport', [
  { photoIds: [p.id], destDir: xmpDest, format: 'rawXmp', createDir: false },
]);
const xtask = await waitTask(xref.taskId);
check(xtask.status === 'completed', `xmp export completed (${xtask.status})`);
const { readFileSync } = await import('node:fs');
const xmpFile = readdirSync(xmpDest).find((f) => f.endsWith('.xmp'));
const xmpText = xmpFile ? readFileSync(join(xmpDest, xmpFile), 'utf8') : '';
check(xmpText.includes('crs:ConvertToGrayscale="True"'), 'sidecar marks grayscale');
check(xmpText.includes('crs:GrayMixerRed="-60"'), 'sidecar carries the gray mixer');

// Leave the photo as we found it.
await call('Edits.ResetEdits', [[p.id]]);
for (const d of dirs) rmSync(d, { recursive: true, force: true });
close();
console.log(failures ? `\n${failures} FAILED` : '\nall checks passed');
process.exit(failures ? 1 : 0);
