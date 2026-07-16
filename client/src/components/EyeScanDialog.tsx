// EyeScanDialog kicks off a folder-wide closed-eye scan: the backend runs
// face detection plus a per-eye open/closed classifier over every photo that
// hasn't been analyzed and scores it into eyes_closed, lighting up the blink
// badges. The SubjectScanDialog shape exactly: one shared background task
// (progress and cancel in the task tray), idempotent server-side, and the
// model weights (two files, well under a megabyte together) are never
// fetched silently — a missing pair is announced up front and allowDownload
// passes only once the user confirms by starting.
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { useApiClient } from '@/api/client';
import { analyzeEyes, eyeModelStatus, type Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { formatBytes } from '@/lib/exif';

// A photo needs the scan until detection has run, whether or not it found a
// face (the no-face sentinel is invisible in eyesClosed, so keying off the
// score would flag faceless frames as pending forever).
const needsScan = (p: Photo) => !p.eyesAnalyzed;

export function EyeScanDialog({
  photos,
  open,
  onOpenChange,
}: {
  photos: Photo[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const client = useApiClient();
  // Eye-model presence, fetched when the dialog opens. null = still checking
  // (or the check failed — we fail open and let the run surface it).
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

  useEffect(() => {
    if (!open) return;
    let live = true;
    eyeModelStatus(client)
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
      // only when the models are actually missing (singleflight, first frame).
      await analyzeEyes(client, ids, true);
    } catch {
      toast.error('Could not start closed-eye detection');
    } finally {
      onOpenChange(false);
    }
  };

  const needDownload = status != null && !status.downloaded && status.bytes > 0;
  const nothingToDo = pendingCount === 0;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onOpenChange(false)}>
      <DialogContent className="max-w-sm" data-testid="eye-scan-dialog">
        <DialogHeader>
          <DialogTitle>Detect closed eyes?</DialogTitle>
          <DialogDescription>
            {nothingToDo
              ? 'Every photo in this folder has already been checked for closed eyes.'
              : `Checks ${pendingCount} photo${pendingCount === 1 ? '' : 's'} in this folder for faces with closed eyes and badges the blinks, so a burst's keeper isn't the one where someone blinked. A soft signal — sunglasses and profiles can misfire. It runs in the background — track progress and cancel from the task tray.`}
          </DialogDescription>
        </DialogHeader>

        {!nothingToDo && needDownload && (
          <p className="text-[12.5px] leading-relaxed text-muted-foreground">
            Requires a one-time download of the face and eye models ({formatBytes(status.bytes)})
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
                data-testid="eye-scan-start"
              >
                {needDownload
                  ? `Download & check ${pendingCount}`
                  : `Check ${pendingCount} photo${pendingCount === 1 ? '' : 's'}`}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
