// InfoPanel is the Info tab of the develop drawer: a larger histogram, a live
// navigator (fed from the shared loupeNav store, so it works even though the
// always-visible drawer covers the floating canvas one), and the photo's
// technical metadata — resolution, file size, camera/EXIF, capture time.
import type { Photo } from '@/api/library';
import { Histogram } from '@/components/Histogram';
import { NavigatorMap } from '@/views/LoupeView';
import { useLoupeNav } from '@/lib/loupeNav';
import { formatAperture, formatBytes, formatCaptured, formatResolution, formatShutter } from '@/lib/exif';

export function InfoPanel({ photo }: { photo: Photo }) {
  const viewport = useLoupeNav((s) => s.viewport);
  const scale = useLoupeNav((s) => s.scale);
  const panTo = useLoupeNav((s) => s.panTo);

  const fileName = photo.fileName.split(/[\\/]/).pop() ?? photo.fileName;

  return (
    <div className="flex flex-col gap-5 px-4 pt-3 pb-4 text-sm">
      <Section title="Histogram">
        <Histogram photo={photo} height={120} className="" />
      </Section>

      <Section title="Navigator" aside={`${Math.round(scale * 100)}%`}>
        <div className="overflow-hidden rounded-lg border bg-inset">
          <NavigatorMap photo={photo} viewport={viewport} onPan={panTo ?? undefined} />
        </div>
      </Section>

      <Section title="Info">
        <dl className="flex flex-col gap-1.5">
          <Row label="File" value={fileName} title={photo.fileName} />
          <Row label="Resolution" value={formatResolution(photo.width, photo.height)} />
          <Row label="File size" value={formatBytes(photo.fileSize)} />
          {photo.sharpness != null && (
            <Row label="Focus score" value={String(Math.round(photo.sharpness))} />
          )}
          {photo.metaLoaded ? (
            <>
              <Row label="Camera" value={[photo.make, photo.model].filter(Boolean).join(' ') || '—'} />
              <Row label="ISO" value={photo.iso > 0 ? String(Math.round(photo.iso)) : '—'} />
              <Row label="Aperture" value={photo.aperture > 0 ? `ƒ/${formatAperture(photo.aperture)}` : '—'} />
              <Row label="Shutter" value={formatShutter(photo.shutter)} />
              <Row label="Focal length" value={photo.focalLen > 0 ? `${Math.round(photo.focalLen)}mm` : '—'} />
              <Row label="Captured" value={formatCaptured(photo.takenAt)} />
            </>
          ) : (
            <span className="text-xs text-faint">Reading metadata…</span>
          )}
        </dl>
      </Section>
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

function Row({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="shrink-0 text-[12px] text-muted-foreground">{label}</dt>
      <dd className="truncate text-right font-mono text-[11.5px]" title={title ?? value}>
        {value}
      </dd>
    </div>
  );
}
