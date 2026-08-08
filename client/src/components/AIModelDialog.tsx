// AIModelDialog is the download-consent gate for AI features: model weights
// are never bundled or fetched silently — the first use of Subject/Depth/
// Scene (or restoring such a mask from a sidecar) opens this dialog with
// what the feature does and how big the one-time download is. The server
// enforces the same rule (GenerateAIMap refuses to download without the
// allowDownload flag this dialog's confirmation sets).
import type { AIKindType } from '@/api/edit';
import { Button } from '@/components/ui/button';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { formatBytes } from '@/lib/bytes';

const AI_KIND_INFO: Record<string, { title: string; feature: string }> = {
  subject: {
    title: 'Subject detection',
    feature: 'Finds the main subject of a photo so you can mask it with one click and adjust it independently of the background.',
  },
  // Background masks ride the subject model, so restoring one asks for the
  // same download — described from the background's side.
  background: {
    title: 'Subject detection',
    feature: 'Finds the main subject of a photo so you can mask everything around it with one click and adjust the background independently.',
  },
  depth: {
    title: 'Depth estimation',
    feature: 'Estimates how far every part of the scene is from the camera, so you can mask by distance — for example lift the foreground or fade the background.',
  },
  class: {
    title: 'Scene detection',
    feature: 'Labels the regions of a photo — sky, people, foliage, water, architecture and more — and offers each detected region as a one-click mask.',
  },
  person: {
    title: 'People detection',
    feature: 'Separates the individual people in a photo, so you can hover over someone and mask just that person — not everyone at once.',
  },
  fill: {
    title: 'content-aware fill',
    feature: 'Synthesizes new pixels from the surround to fill a region — a retouch spot, or everything a mask covers — for blemishes and distractions where no clean source patch exists to heal or clone from.',
  },
};

export interface PendingAIDownload {
  // An AI-mask kind, or 'fill' for the retouch inpainting model.
  kind: AIKindType | 'fill';
  bytes: number;
  // What confirmed consent should do: add a fresh mask ('add') or just
  // regenerate the maps an existing mask references ('restore').
  mode: 'add' | 'restore';
  // Which mask the subject matte should become: the subject itself, or its
  // inverse pre-loaded with the background-defocus recipe. Only meaningful
  // for kind 'subject'.
  variant?: 'subject' | 'background';
}

export function AIModelDialog({
  pending,
  onConfirm,
  onCancel,
}: {
  pending: PendingAIDownload | null;
  onConfirm: (p: PendingAIDownload) => void;
  onCancel: () => void;
}) {
  const info = pending ? AI_KIND_INFO[pending.kind] : undefined;
  return (
    <Dialog open={pending != null} onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-w-sm" data-testid="ai-model-dialog">
        <DialogHeader>
          <DialogTitle>Download {info?.title.toLowerCase()}?</DialogTitle>
          <DialogDescription>{info?.feature}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2 text-[12.5px] leading-relaxed">
          {pending?.mode === 'restore' && (
            <p>
              This photo&apos;s edit includes such a mask (for example from
              another computer&apos;s sidecar); without the model it stays
              inactive.
            </p>
          )}
          <p className="text-muted-foreground">
            This runs entirely on your computer. It needs a one-time download
            of the model ({pending ? formatBytes(pending.bytes) : ''}) from
            marraw&apos;s model repository, stored locally and never fetched
            again. Nothing about your photos leaves this machine.
          </p>
        </div>
        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={onCancel}>
            Not now
          </Button>
          <Button size="sm" onClick={() => pending && onConfirm(pending)} data-testid="ai-model-download">
            Download {pending ? formatBytes(pending.bytes) : ''}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
