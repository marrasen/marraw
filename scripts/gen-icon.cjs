// Generates the marraw app icon from a single computed SVG master:
//   assets/icon.svg         — vector master (source of truth, committed)
//   assets/icon.png         — 1024px PNG (docs / BrowserWindow / non-win builds)
//   assets/icon.ico         — multi-size Windows icon (16..256) for the installer
//   client/public/icon.svg  — favicon served by the client
//
// assets/ is committed on purpose — build/ is a gitignored scratch dir, so the
// installer icon must live somewhere tracked or a fresh `npm run dist` breaks.
//
// No ImageMagick / sharp on this machine, so we rasterise with the Electron
// Chromium that's already a dependency: this file IS an Electron main process.
// Run it via:  npm run gen:icon   (see package.json — clears ELECTRON_RUN_AS_NODE).
//
// The mark is a photographic aperture/iris (the app's own Lucide glyph family)
// in white/indigo facets on the brand indigo tile (#7c83ff dark / #5b62e6 light).
const { app, BrowserWindow } = require('electron');
const fs = require('node:fs');
const path = require('node:path');

const ROOT = path.join(__dirname, '..');
const OUT_ASSETS = path.join(ROOT, 'assets');
const OUT_PUBLIC = path.join(ROOT, 'client', 'public');

// ---- Vector master -------------------------------------------------------
// viewBox is 512; everything below is authored in that space.
const VB = 512;
const C = 256; // centre
const R = 188; // outer blade radius (lens rim)
const A = 84; // hexagonal opening radius
const PHI = 32; // ring twist vs. the hexagon → the iris "pinwheel"
const N = 6;

const rad = (d) => (d * Math.PI) / 180;
const pt = (angDeg, r) => [C + r * Math.cos(rad(angDeg)), C + r * Math.sin(rad(angDeg))];
const f = (n) => n.toFixed(2);

function buildSvg() {
  // Six blades tile the annulus between the hexagon opening and the rim;
  // alternating fills give the iris its faceted, folded-metal depth.
  const blades = [];
  for (let i = 0; i < N; i++) {
    const a0 = i * 60 - 90; // -90 puts a hexagon vertex straight up
    const a1 = (i + 1) * 60 - 90;
    const [vx0, vy0] = pt(a0, A); // hexagon vertex
    const [vx1, vy1] = pt(a1, A);
    const [rx0, ry0] = pt(a0 + PHI, R); // twisted rim point
    const [rx1, ry1] = pt(a1 + PHI, R);
    const d = `M${f(vx0)} ${f(vy0)} L${f(rx0)} ${f(ry0)} A${R} ${R} 0 0 1 ${f(rx1)} ${f(ry1)} L${f(vx1)} ${f(vy1)} Z`;
    const fill = i % 2 === 0 ? 'url(#blade)' : 'url(#bladeDim)';
    blades.push(`<path d="${d}" fill="${fill}"/>`);
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${VB} ${VB}" width="${VB}" height="${VB}">
  <defs>
    <linearGradient id="tile" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="#8f95ff"/>
      <stop offset="1" stop-color="#545cde"/>
    </linearGradient>
    <radialGradient id="glow" cx="0.32" cy="0.26" r="0.9">
      <stop offset="0" stop-color="#ffffff" stop-opacity="0.28"/>
      <stop offset="0.55" stop-color="#ffffff" stop-opacity="0"/>
    </radialGradient>
    <linearGradient id="blade" x1="0" y1="0" x2="0.6" y2="1">
      <stop offset="0" stop-color="#ffffff"/>
      <stop offset="1" stop-color="#eef0ff"/>
    </linearGradient>
    <linearGradient id="bladeDim" x1="0" y1="0" x2="0.6" y2="1">
      <stop offset="0" stop-color="#d3d7ff"/>
      <stop offset="1" stop-color="#b3baff"/>
    </linearGradient>
  </defs>
  <rect x="0" y="0" width="${VB}" height="${VB}" rx="112" ry="112" fill="url(#tile)"/>
  <rect x="0" y="0" width="${VB}" height="${VB}" rx="112" ry="112" fill="url(#glow)"/>
  <g>
    ${blades.join('\n    ')}
    <circle cx="${C}" cy="${C}" r="${R}" fill="none" stroke="#3f46c9" stroke-opacity="0.35" stroke-width="4"/>
  </g>
</svg>`;
}

// ---- ICO packing ---------------------------------------------------------
// Small sizes ship as classic 32-bit BMP DIBs (crispest in Explorer);
// 64px+ ship as embedded PNG to keep the file small.
function bmpEntry(width, height, bgraTopDown) {
  const rowBytes = width * 4;
  const xor = Buffer.alloc(rowBytes * height);
  for (let y = 0; y < height; y++) {
    const src = (height - 1 - y) * rowBytes; // BMP rows run bottom-up
    bgraTopDown.copy(xor, y * rowBytes, src, src + rowBytes);
  }
  const andStride = Math.ceil(width / 8 / 4) * 4; // 1bpp mask, 4-byte aligned
  const andMask = Buffer.alloc(andStride * height, 0); // alpha handles cutout
  const header = Buffer.alloc(40);
  header.writeUInt32LE(40, 0);
  header.writeInt32LE(width, 4);
  header.writeInt32LE(height * 2, 8); // XOR + AND stacked
  header.writeUInt16LE(1, 12);
  header.writeUInt16LE(32, 14);
  header.writeUInt32LE(0, 16); // BI_RGB
  header.writeUInt32LE(xor.length, 20);
  return Buffer.concat([header, xor, andMask]);
}

function packIco(entries) {
  const head = Buffer.alloc(6);
  head.writeUInt16LE(0, 0);
  head.writeUInt16LE(1, 2); // type: icon
  head.writeUInt16LE(entries.length, 4);
  const dir = Buffer.alloc(16 * entries.length);
  let offset = 6 + dir.length;
  entries.forEach((e, i) => {
    const o = i * 16;
    dir.writeUInt8(e.size >= 256 ? 0 : e.size, o + 0);
    dir.writeUInt8(e.size >= 256 ? 0 : e.size, o + 1);
    dir.writeUInt8(0, o + 2); // palette
    dir.writeUInt8(0, o + 3); // reserved
    dir.writeUInt16LE(1, o + 4); // planes
    dir.writeUInt16LE(32, o + 6); // bpp
    dir.writeUInt32LE(e.data.length, o + 8);
    dir.writeUInt32LE(offset, o + 12);
    offset += e.data.length;
  });
  return Buffer.concat([head, dir, ...entries.map((e) => e.data)]);
}

async function run() {
  const svg = buildSvg();
  fs.mkdirSync(OUT_ASSETS, { recursive: true });
  fs.mkdirSync(OUT_PUBLIC, { recursive: true });
  fs.writeFileSync(path.join(OUT_ASSETS, 'icon.svg'), svg);
  fs.writeFileSync(path.join(OUT_PUBLIC, 'icon.svg'), svg);

  // Render the SVG at 1024 in a real (off-screen-positioned) transparent
  // window — show:true guarantees Chromium actually paints before capture.
  const win = new BrowserWindow({
    x: -4000,
    y: -4000,
    width: 1024,
    height: 1024,
    show: true,
    frame: false,
    transparent: true,
    skipTaskbar: true,
    resizable: false,
    webPreferences: { offscreen: false },
  });
  win.setContentSize(1024, 1024);
  const html = `<!doctype html><html><head><meta charset="utf-8"><style>html,body{margin:0;padding:0;background:transparent}svg{display:block;width:1024px;height:1024px}</style></head><body>${svg}</body></html>`;
  await win.loadURL('data:text/html;charset=utf-8,' + encodeURIComponent(html));

  // capturePage races the compositor's first paint — poll until it hands back
  // a non-empty frame rather than trusting a single fixed delay.
  let shot = null;
  for (let attempt = 0; attempt < 40; attempt++) {
    await new Promise((r) => setTimeout(r, 150));
    const cap = await win.capturePage({ x: 0, y: 0, width: 1024, height: 1024 });
    if (!cap.isEmpty() && cap.getSize().width > 0) {
      shot = cap;
      break;
    }
  }
  if (!shot) throw new Error('capturePage returned an empty image after 40 attempts');
  const master = shot.getSize().width === 1024 ? shot : shot.resize({ width: 1024, height: 1024, quality: 'best' });
  fs.writeFileSync(path.join(OUT_ASSETS, 'icon.png'), master.toPNG());

  const entries = [];
  for (const size of [16, 24, 32, 48, 64, 128, 256]) {
    const img = master.resize({ width: size, height: size, quality: 'best' });
    const data = size <= 48 ? bmpEntry(size, size, img.toBitmap()) : img.toPNG();
    entries.push({ size, data });
  }
  fs.writeFileSync(path.join(OUT_ASSETS, 'icon.ico'), packIco(entries));

  win.destroy();
  console.log('ICON_OK assets/icon.svg assets/icon.png assets/icon.ico client/public/icon.svg');
}

app.whenReady().then(() =>
  run().then(
    () => app.exit(0),
    (err) => {
      console.error('ICON_FAIL', err && err.stack ? err.stack : err);
      app.exit(1);
    },
  ),
);
