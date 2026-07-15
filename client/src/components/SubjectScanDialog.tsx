// SubjectScanDialog runs a folder-wide subject analysis: it generates the AI
// subject matte for every photo that lacks one, which the backend scores into
// subject_sharpness immediately and patches straight back to the grid — so the
// focus badges re-evaluate live, subject-aware, as the scan progresses.
//
// GenerateAIMap is idempotent (a photo whose matte is already on disk returns
// without running inference), so the loop only pays for the frames that need
// it. Model weights are never fetched silently: when the subject model isn't on
// disk this dialog says so up front and only passes allowDownload once the user
// confirms. The scan aborts if the dialog closes (Cancel, or a folder switch).
import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { useApiClient } from '@/api/client';
import { AIKind } from '@/api/edit';
import { aIModelStatus as aiModelStatus, generateAIMap } from '@/api/edits';
import type { Photo } from '@/api/library';
import { Button } from '@/components/ui/button';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { formatBytes } from '@/lib/exif';

// A photo needs analysis when it has no subject-aware score yet. (An
// unscoreable frame reads the same and re-runs, but that hits GenerateAIMap's
// on-disk fast path, so it costs a round trip and no inference.)
const needsScan = (p: Photo) => p.subjectSharpness == null;

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
  const [running, setRunning] = useState(false);
  const [done, setDone] = useState(0);
  const [total, setTotal] = useState(0);
  const abortRef = useRef<AbortController | null>(null);

  const pending = photos.filter(needsScan).length;

  // Reset transient state the moment the dialog opens — adjust during render
  // (open is a primitive, so no render loop), not an effect.
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) {
      setStatus(null);
      setDone(0);
      setTotal(0);
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

  // Abort an in-flight scan when the dialog closes (Cancel / folder switch); the
  // run loop's own catch then flips `running` off.
  useEffect(() => {
    if (open) return;
    abortRef.current?.abort();
    abortRef.current = null;
  }, [open]);

  const run = async () => {
    const work = photos.filter(needsScan);
    if (work.length === 0) {
      onOpenChange(false);
      return;
    }
    const ac = new AbortController();
    abortRef.current = ac;
    setTotal(work.length);
    setDone(0);
    setRunning(true);
    let failures = 0;
    for (const p of work) {
      if (ac.signal.aborted) break;
      try {
        // allowDownload is safe to always set post-consent: it only fetches
        // when the model is actually missing (singleflight, first photo only).
        await generateAIMap(client, p.id, AIKind.Subject, true, { signal: ac.signal });
      } catch {
        if (ac.signal.aborted) break;
        failures += 1;
      }
      setDone((n) => n + 1);
    }
    abortRef.current = null;
    setRunning(false);
    if (ac.signal.aborted) return; // dialog already closing
    if (failures > 0) toast.warning(`Subject scan finished with ${failures} error${failures === 1 ? '' : 's'}`);
    else toast.success('Subjects analyzed — focus re-scored');
    onOpenChange(false);
  };

  const needDownload = status != null && !status.downloaded && status.bytes > 0;
  const pct = total > 0 ? Math.round((done / total) * 100) : 0;
  const nothingToDo = pending === 0;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onOpenChange(false)}>
      <DialogContent className="max-w-sm" data-testid="subject-scan-dialog">
        <DialogHeader>
          <DialogTitle>Analyze subjects &amp; re-score focus?</DialogTitle>
          <DialogDescription>
            {nothingToDo
              ? 'Every photo in this folder already has a subject-aware focus score.'
              : `Finds the main subject of ${pending} photo${pending === 1 ? '' : 's'} in this folder and re-scores focus over the subject alone, so a sharp background can’t hide a soft subject.`}
          </DialogDescription>
        </DialogHeader>

        {!nothingToDo && (
          <div className="flex flex-col gap-2 text-[12.5px] leading-relaxed">
            {needDownload && !running && (
              <p className="text-muted-foreground">
                Requires a one-time download of the subject model (
                {formatBytes(status.bytes)}) from marraw’s model repository. It
                runs entirely on your computer — nothing about your photos leaves
                this machine.
              </p>
            )}
            {running && (
              <div className="flex flex-col gap-1.5">
                <div className="h-1 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full bg-primary transition-all"
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <span className="font-mono text-[11px] text-muted-foreground tabular-nums">
                  {done === 0 && needDownload ? 'Downloading model…' : `Analyzing… ${done} / ${total}`}
                </span>
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          {nothingToDo ? (
            <Button size="sm" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          ) : (
            <>
              <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
                {running ? 'Cancel' : 'Not now'}
              </Button>
              <Button
                size="sm"
                disabled={running || status == null}
                onClick={() => void run()}
                data-testid="subject-scan-start"
              >
                {needDownload
                  ? `Download & analyze ${pending}`
                  : `Analyze ${pending} photo${pending === 1 ? '' : 's'}`}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
