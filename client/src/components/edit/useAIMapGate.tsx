// The download-consent gate every AI-map feature goes through: model weights
// are never fetched silently, so the first use of Subject / Depth / Scene /
// People has to check whether the model is on disk, ask if it isn't, and only
// then generate the map. That sequence is identical wherever it happens — the
// masks list adds a mask with the returned version, the Effects group switches
// tilt shift on with it — and only the last step differs, so the last step is
// the callback and everything before it lives here.
import { useRef, useState } from 'react';
import { toast } from 'sonner';
// (aprot's camelCasing lowercases exactly one leading character: aIModelStatus.)
import { aIModelStatus as aiModelStatus, generateAIMap } from '@/api/edits';
import type { AIMapResult } from '@/api/edits';
import type { AIKindType } from '@/api/edit';
import { AIModelDialog, type PendingAIDownload } from '@/components/AIModelDialog';
import { type ApiClient } from '@/api/client';

export interface AIMapGate {
  // request runs `then` with the generated map, asking for download consent
  // first when the model isn't on disk. mode only shapes the dialog's copy
  // (see PendingAIDownload); variant rides along for the subject/background
  // pair, whose two masks come off one model.
  request: (
    kind: AIKindType,
    then: (res: AIMapResult) => void,
    opts?: { mode?: 'add' | 'restore'; variant?: 'subject' | 'background' },
  ) => void;
  // The kind currently generating, for the button spinners; null when idle.
  generating: AIKindType | null;
  // Render this once in the section — it is inert until consent is pending.
  dialog: React.ReactNode;
}

export function useAIMapGate(client: ApiClient, photoId: number | null): AIMapGate {
  const [generating, setGenerating] = useState<AIKindType | null>(null);
  // Non-null renders the dialog; consent resumes the request it belongs to.
  const [pending, setPending] = useState<PendingAIDownload | null>(null);
  // What to do once the map exists. Held in a ref rather than in `pending` so
  // it survives the consent round trip without re-rendering, and so a caller's
  // closure over fresh props is the one that runs.
  const thenRef = useRef<((res: AIMapResult) => void) | null>(null);

  const run = async (kind: AIKindType, allowDownload: boolean) => {
    if (photoId == null) return;
    setGenerating(kind);
    try {
      const res = await generateAIMap(client, photoId, kind, allowDownload);
      thenRef.current?.(res);
    } catch (err) {
      toast.error(`AI model failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setGenerating(null);
    }
  };

  const request: AIMapGate['request'] = (kind, then, opts) => {
    if (photoId == null || generating) return;
    thenRef.current = then;
    void (async () => {
      try {
        const status = await aiModelStatus(client, kind);
        if (!status.downloaded) {
          setPending({ kind, bytes: status.bytes, mode: opts?.mode ?? 'add', variant: opts?.variant });
          return;
        }
      } catch (err) {
        toast.error(`AI model failed: ${err instanceof Error ? err.message : String(err)}`);
        return;
      }
      void run(kind, false);
    })();
  };

  const dialog = (
    <AIModelDialog
      pending={pending}
      onConfirm={(p) => {
        setPending(null);
        // Only the retouch fill gate parks 'fill', and it does not come
        // through here — every kind this hook sees is an AI-map kind.
        if (p.kind !== 'fill') void run(p.kind, true);
      }}
      onCancel={() => setPending(null)}
    />
  );

  return { request, generating, dialog };
}
