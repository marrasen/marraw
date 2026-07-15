// SubjectScanDialog kicks off a folder-wide subject analysis: it asks the
// backend to generate the AI subject matte for every photo that lacks one and
// score it into subject_sharpness, so the grid's focus badges re-evaluate
// subject-aware. The work runs as ONE shared background task — progress and
// cancel live in the task tray, like exports and the pre-render passes — rather
// than a per-photo task (and toast) per frame, which used to flood the
// notifications when scanning a whole folder.
//
// The scan is idempotent (a photo whose matte is already scored short-circuits
// server-side), so it only pays for the frames that need it. Model weights are
// never fetched silently: when the subject model isn't on disk this dialog says
// so up front and only passes allowDownload once the user confirms by starting.
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { useApiClient } from '@/api/client';
import { AIKind } from '@/api/edit';
import { aIModelStatus as aiModelStatus, analyzeSubjects } from '@/api/edits';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { formatBytes } from '@/lib/exif';

// A photo needs analysis until its subject matte has been measured. This keys
// off subjectAnalyzed, NOT the score: a frame with no detectable subject scores
// invisibly, so keying off the score would flag it as pending forever and the
// backend would skip it (already measured) — the scan would never resolve.
const needsScan = (p: Photo) => !p.subjectAnalyzed;

export function SubjectScanDialog({
  photos,
  open,
  onOpenChange,
}: {
  photos: Photo[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const client = useApiClient();
  // Subject model presence, fetched when the dialog opens. null = still
  // checking (or the check failed — we fail open and let the run surface it).
  const [status, setStatus] = useState<{ downloaded: boolean; bytes: number } | null>(null);
  const [starting, setStarting] = useState(false);

  const pending = photos.filter(needsScan);
  const pendingCount = pending.length;

  // Reset transient state the moment the dialog opens — adjust during render
  // (open is a primitive, so no render loop), not an effect.
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) {
      setStatus(null);
      setStarting(false);
    }
  }

  // Check model presence each time the dialog opens. setState lives only in the
  // async callbacks, never synchronously in the effect body.
  useEffect(() => {
    if (!open) return;
    let live = true;
    aiModelStatus(client, AIKind.Subject)
      .then((s) => live && setStatus({ downloaded: s.downloaded, bytes: s.bytes }))
      .catch(() => live && setStatus({ downloaded: true, bytes: 0 }));
    return () => {
      live = false;
    };
  }, [client, open]);

  const start = async () => {
    const ids = pending.map((p) => p.id);
    if (ids.length === 0) {
      onOpenChange(false);
      return;
    }
    setStarting(true);
    try {
      // allowDownload is safe to always set post-consent: the backend fetches
      // only when the model is actually missing (singleflight, first frame).
      // The task runs in the background — the tray owns progress and cancel.
      await analyzeSubjects(client, ids, true);
    } catch {
      toast.error('Could not start subject analysis');
    } finally {
      onOpenChange(false);
    }
  };

  const needDownload = status != null && !status.downloaded && status.bytes > 0;
  const nothingToDo = pendingCount === 0;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onOpenChange(false)}>
      <DialogContent className="max-w-sm" data-testid="subject-scan-dialog">
        <DialogHeader>
          <DialogTitle>Analyze subjects &amp; re-score focus?</DialogTitle>
          <DialogDescription>
            {nothingToDo
              ? 'Every photo in this folder has already been analyzed for subjects.'
              : `Finds the main subject of ${pendingCount} photo${pendingCount === 1 ? '' : 's'} in this folder and re-scores focus over the subject alone, so a sharp background can’t hide a soft subject. It runs in the background — track progress and cancel from the task tray.`}
          </DialogDescription>
        </DialogHeader>

        {!nothingToDo && needDownload && (
          <p className="text-[12.5px] leading-relaxed text-muted-foreground">
            Requires a one-time download of the subject model ({formatBytes(status.bytes)})
            from marraw’s model repository. It runs entirely on your computer —
            nothing about your photos leaves this machine.
          </p>
        )}

        <DialogFooter>
          {nothingToDo ? (
            <Button size="sm" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          ) : (
            <>
              <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
                Not now
              </Button>
              <Button
                size="sm"
                disabled={starting || status == null}
                onClick={() => void start()}
                data-testid="subject-scan-start"
              >
                {needDownload
                  ? `Download & analyze ${pendingCount}`
                  : `Analyze ${pendingCount} photo${pendingCount === 1 ? '' : 's'}`}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
