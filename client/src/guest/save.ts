import { downloadUrl, zipUrl } from '@/lib/backend';

// Saving photos from a phone.
//
// A plain <a download> is the desktop answer and the wrong one on iOS, where
// it opens the JPEG in a viewer instead of putting it anywhere. The share
// sheet is the route to "Save Image" / "Add to Photos", so prefer it whenever
// the browser will take a file — and fall back to the link elsewhere.

function viaLink(href: string, fileName?: string) {
  const a = document.createElement('a');
  a.href = href;
  if (fileName) a.download = fileName;
  a.rel = 'noreferrer';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

export async function savePhoto(id: number, fileName: string): Promise<void> {
  const name = fileName.replace(/\.[^.]+$/, '') + '.jpg';
  // canShare({files}) is the only reliable capability test: Android Chrome
  // has navigator.share but refuses files, and iOS Safari accepts them.
  const shareFiles = typeof navigator.canShare === 'function' && typeof navigator.share === 'function';
  if (!shareFiles) {
    viaLink(downloadUrl(id), name);
    return;
  }
  const res = await fetch(downloadUrl(id));
  if (!res.ok) throw new Error(`download failed (${res.status})`);
  const file = new File([await res.blob()], name, { type: 'image/jpeg' });
  if (!navigator.canShare({ files: [file] })) {
    viaLink(downloadUrl(id), name);
    return;
  }
  try {
    await navigator.share({ files: [file] });
  } catch (err) {
    // Dismissing the sheet rejects; that is not a failure to report.
    if ((err as Error)?.name !== 'AbortError') viaLink(downloadUrl(id), name);
  }
}

// A selection goes as one archive. No share sheet: phones have nothing useful
// to do with a zip, and every platform's browser can download one.
export function saveSelection(ids: number[]): void {
  viaLink(zipUrl(ids));
}
