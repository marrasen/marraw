import { toast } from 'sonner';
import type { ApiClient } from '@/api/client';
import { renderClipboard, type ClipboardRenderRequest } from '@/api/export';
import { useUIStore, selectionOrFocus } from '@/stores/uiStore';

// Renders one photo server-side (PNG, sRGB, EXIF-free) and puts it on the
// system clipboard. Prefers the Electron bridge: the native clipboard has no
// document-focus requirement, so the copy still lands if the user alt-tabs
// to the target app while the multi-second render runs. The browser path
// (dev tabs) needs the document focused for the whole render.
export async function copyPhotoToClipboard(
  client: ApiClient,
  req: ClipboardRenderRequest,
  signal?: AbortSignal,
): Promise<void> {
  const blob = await renderClipboard(client, req, { signal });
  if (window.marraw?.copyImageToClipboard) {
    const ok = await window.marraw.copyImageToClipboard(await blob.arrayBuffer());
    if (!ok) throw new Error('could not write the image to the clipboard');
    return;
  }
  try {
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
  } catch (err) {
    if ((err as DOMException).name === 'NotAllowedError') {
      throw new Error('clipboard blocked — keep this window focused and try again', {
        cause: err,
      });
    }
    throw err;
  }
}

// Copies the single selected/focused photo using the last-used export
// settings, with toast progress — the Ctrl+Shift+C / command-palette action.
// Returns false (without side effects) unless exactly one photo is targeted.
export function copyTargetPhotoToClipboard(client: ApiClient): boolean {
  const target = selectionOrFocus();
  if (target.length !== 1) return false;
  const o = useUIStore.getState().exportOptions;
  toast.promise(
    copyPhotoToClipboard(client, {
      photoId: target[0],
      longEdge: o.resizeMode === 'edge' ? o.edgePx : 0,
      sharpenTarget: o.sharpenTarget,
      sharpenAmount: o.sharpenAmount,
      watermarkId: o.watermarkId,
    }),
    {
      loading: 'Rendering for clipboard…',
      success: 'Copied — ready to paste',
      error: (err) => `Copy failed: ${(err as Error).message}`,
    },
  );
  return true;
}
