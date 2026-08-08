// InfoPanel is the Info tab of the develop drawer: a larger histogram, a live
// navigator (fed from the shared loupeNav store, so it works even though the
// always-visible drawer covers the floating canvas one), the photo's technical
// metadata — resolution, file size, camera/EXIF, capture time — and the
// actions that take that identity elsewhere (reveal on disk, copy).
// The Library aside reuses it with showNavigator off: no loupe image is
// mounted there, so the map would show whatever viewport was left behind.
import { useMemo } from 'react';
import { toast } from 'sonner';
import { Copy, ExternalLink, FileText, Folder } from 'lucide-react';
import type { Photo } from '@/api/library';
import { canUseHostFs } from '@/lib/backend';
import { focusRank, softThreshold } from '@/lib/bursts';
import { EYES_CLOSED_BADGE } from '@/lib/eyes';
import { useFeature } from '@/lib/features';
import { joinPath, parentPath } from '@/lib/library';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Histogram } from '@/components/Histogram';
import { NavigatorMap } from '@/views/LoupeView';
import { useLoupeNav } from '@/lib/loupeNav';
import { useUIStore } from '@/stores/uiStore';
import { formatBytes } from '@/lib/bytes';
import { formatAperture, formatCaptured, formatResolution, formatShutter } from '@/lib/exif';

// What the focus numbers actually are, in the tooltip that carries them —
// a bare Laplacian variance is unreadable without this.
const FOCUS_HINT =
  'Edge contrast across the whole frame (Laplacian variance). Higher is sharper, but the scale follows the scene, so the number only means something next to the rest of this shoot.';
const SUBJECT_HINT =
  'The same measurement taken inside the detected subject only, so a sharp background cannot hide a soft subject. It naturally runs lower than the whole-frame score — compare it with other subject scores, not with that one.';

// One row of the info list. `meter` renders under it (the focus scores'
// shoot-relative bar); `hint` explains the label on hover.
interface InfoRow {
  label: string;
  value: string;
  title?: string;
  hint?: string;
  meter?: React.ReactNode;
}

// photos is the WHOLE folder (both mount sites already hold it): the focus
// scores are only readable against their own shoot, so the meters need the
// population, not just this frame.
export function InfoPanel({
  photo,
  photos = [],
  showNavigator = true,
}: {
  photo: Photo;
  photos?: Photo[];
  showNavigator?: boolean;
}) {
  const viewport = useLoupeNav((s) => s.viewport);
  const scale = useLoupeNav((s) => s.scale);
  const panTo = useLoupeNav((s) => s.panTo);
  // The HUD and top bar show only the folder NAME; the full path lives here.
  const folderPath = useUIStore((s) => s.folderPath);
  const subjectsEnabled = useFeature('subjects');
  const eyesEnabled = useFeature('eyes');
  const softEnabled = useFeature('softFilter');

  const fileName = photo.fileName.split(/[\\/]/).pop() ?? photo.fileName;
  // fileName is relative to the opened folder and keeps its subpath when the
  // shoot was scanned recursively, so the file's own directory is not always
  // the folder root — only walk up when there is a subpath to walk up from.
  const filePath = folderPath ? joinPath(folderPath, photo.fileName) : null;
  const dirPath = filePath && folderPath ? (/[\\/]/.test(photo.fileName) ? parentPath(filePath) : folderPath) : null;

  // Score populations, one per metric — never merged (see focusRank).
  const frameScores = useMemo(
    () => photos.map((p) => p.sharpness).filter((v): v is number => v != null),
    [photos],
  );
  const subjectScores = useMemo(
    () => photos.map((p) => p.subjectSharpness).filter((v): v is number => v != null),
    [photos],
  );
  const softBelow = useMemo(() => (softEnabled ? softThreshold(photos) : 0), [photos, softEnabled]);
  const cutoffHint = softBelow > 0 ? ` Frames scoring under ${Math.round(softBelow)} are badged soft here.` : '';

  const rows: InfoRow[] = [{ label: 'File', value: fileName, title: photo.fileName }];
  if (dirPath) rows.push({ label: 'Folder', value: dirPath });
  rows.push(
    { label: 'Resolution', value: formatResolution(photo.width, photo.height) },
    { label: 'File size', value: formatBytes(photo.fileSize) },
  );
  if (photo.sharpness != null) {
    rows.push({
      label: 'Focus score',
      value: String(Math.round(photo.sharpness)),
      hint: FOCUS_HINT + cutoffHint,
      meter: (
        <FocusMeter
          score={photo.sharpness}
          population={frameScores}
          // Which row the soft badge is judging: isSoft prefers the subject
          // score whenever there is one, so the whole-frame row only carries
          // the verdict when there isn't.
          soft={softBelow > 0 && photo.subjectSharpness == null && photo.sharpness < softBelow}
          hint={FOCUS_HINT + cutoffHint}
        />
      ),
    });
  }
  if (subjectsEnabled && photo.subjectSharpness != null) {
    rows.push({
      label: 'Subject focus',
      value: String(Math.round(photo.subjectSharpness)),
      hint: SUBJECT_HINT + cutoffHint,
      meter: (
        <FocusMeter
          score={photo.subjectSharpness}
          population={subjectScores}
          soft={softBelow > 0 && photo.subjectSharpness < softBelow}
          hint={SUBJECT_HINT + cutoffHint}
        />
      ),
    });
  }
  if (eyesEnabled && photo.eyesAnalyzed) {
    rows.push({
      label: 'Eyes',
      value:
        photo.eyesClosed == null
          ? 'No face found'
          : photo.eyesClosed >= EYES_CLOSED_BADGE
            ? `Closed? (${Math.round(photo.eyesClosed * 100)}%)`
            : 'Open',
    });
  }
  if (photo.metaLoaded) {
    rows.push(
      { label: 'Camera', value: [photo.make, photo.model].filter(Boolean).join(' ') || '—' },
      { label: 'ISO', value: photo.iso > 0 ? String(Math.round(photo.iso)) : '—' },
      { label: 'Aperture', value: photo.aperture > 0 ? `ƒ/${formatAperture(photo.aperture)}` : '—' },
      { label: 'Shutter', value: formatShutter(photo.shutter) },
      { label: 'Focal length', value: photo.focalLen > 0 ? `${Math.round(photo.focalLen)}mm` : '—' },
      { label: 'Captured', value: formatCaptured(photo.takenAt) },
    );
  }

  return (
    <div className="flex flex-col gap-5 px-4 pt-3 pb-4 text-sm">
      <Section title="Histogram">
        <Histogram photo={photo} height={120} className="" />
      </Section>

      {showNavigator && (
        <Section title="Navigator" aside={`${Math.round(scale * 100)}%`}>
          <div className="overflow-hidden rounded-lg border bg-inset">
            <NavigatorMap photo={photo} viewport={viewport} onPan={panTo ?? undefined} />
          </div>
        </Section>
      )}

      <Section title="Info">
        <dl className="flex flex-col gap-1.5">
          {rows.map((row) => (
            <Row key={row.label} {...row} />
          ))}
        </dl>
        {!photo.metaLoaded && <span className="text-xs text-faint">Reading metadata…</span>}

        {/* Copy takes the list exactly as shown, so a paste says the same
            thing the panel does — no hidden fields, none of the ones the
            metadata backfill has not filled in yet. */}
        <div className="flex flex-wrap gap-1.5 pt-1">
          {filePath && canUseHostFs() && (
            <Button size="sm" variant="outline" onClick={() => window.marraw?.revealInExplorer(filePath)}>
              <ExternalLink data-icon="inline-start" />
              Locate on disk
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            onClick={() => copyText(rows.map((r) => `${r.label}: ${r.value}`).join('\n'), 'Info')}
          >
            <Copy data-icon="inline-start" />
            Copy
          </Button>
          {dirPath && (
            <Button size="sm" variant="outline" onClick={() => copyText(dirPath, 'Folder path')}>
              <Folder data-icon="inline-start" />
              Copy folder path
            </Button>
          )}
          <Button size="sm" variant="outline" onClick={() => copyText(fileName, 'File name')}>
            <FileText data-icon="inline-start" />
            Copy filename
          </Button>
        </div>
      </Section>
    </div>
  );
}

// One clipboard path for all three copy buttons, so a blocked clipboard says
// so instead of leaving the click looking like it worked.
function copyText(text: string, what: string) {
  void navigator.clipboard.writeText(text).then(
    () => toast.success(`${what} copied`),
    () => toast.error(`Could not copy ${what.toLowerCase()}`),
  );
}

// FocusMeter answers "is 3184 any good?". The score is Laplacian variance,
// which scales with how much texture the scene holds, so the honest reading is
// a position within the shoot — plus the soft verdict when this is the frame's
// judged metric, matching the grid's badge rather than second-guessing it.
function FocusMeter({
  score,
  population,
  soft,
  hint,
}: {
  score: number;
  population: number[];
  soft: boolean;
  hint: string;
}) {
  const rank = focusRank(population, score);
  // Too few measurements to place it and nothing to warn about: the number
  // stands alone rather than under an empty bar.
  if (rank == null && !soft) return null;
  const pct = rank == null ? null : Math.round(rank * 100);
  return (
    // Right-aligned under the value it annotates: full-width it reads as a
    // rule between rows rather than as this score's meter.
    <div className="flex items-center justify-end gap-2" title={hint}>
      <div className="relative h-1 w-16 overflow-hidden rounded-full bg-inset">
        <div
          className={cn('absolute inset-y-0 left-0 rounded-full', soft ? 'bg-amber-400' : 'bg-primary')}
          style={{ width: `${(rank ?? 0) * 100}%` }}
        />
      </div>
      <span className={cn('shrink-0 text-[10px]', soft ? 'text-amber-400' : 'text-faint')}>
        {soft ? 'soft for this shoot' : `sharper than ${pct}%`}
      </span>
    </div>
  );
}

function Section({
  title,
  aside,
  children,
}: {
  title: string;
  aside?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <span className="text-[10px] tracking-[.07em] text-muted-foreground uppercase">{title}</span>
        {aside && <span className="font-mono text-[10.5px] text-accent-text tabular-nums">{aside}</span>}
      </div>
      {children}
    </div>
  );
}

function Row({ label, value, title, hint, meter }: InfoRow) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-3">
        <dt
          className={cn('shrink-0 text-[12px] text-muted-foreground', hint && 'cursor-help underline decoration-dotted underline-offset-2')}
          title={hint}
        >
          {label}
        </dt>
        <dd className="truncate text-right font-mono text-[11.5px]" title={title ?? value}>
          {value}
        </dd>
      </div>
      {meter}
    </div>
  );
}
