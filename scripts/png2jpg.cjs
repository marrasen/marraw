// PNG→JPEG conversion via Electron's nativeImage (no imagemagick on the box):
//   electron scripts/png2jpg.cjs src.png=dst.jpg [more pairs...]
const { app, nativeImage } = require('electron');
const fs = require('node:fs');

app.disableHardwareAcceleration();
app.whenReady().then(() => {
  for (const pair of process.argv.slice(2)) {
    const [src, dst] = pair.split('=');
    const img = nativeImage.createFromPath(src);
    if (img.isEmpty()) {
      console.error(`png2jpg: could not read ${src}`);
      app.exit(1);
      return;
    }
    fs.writeFileSync(dst, img.toJPEG(90));
    console.log(`${dst} ${fs.statSync(dst).size} bytes`);
  }
  app.exit(0);
});
