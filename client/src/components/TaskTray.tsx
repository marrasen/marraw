import { Loader2 } from 'lucide-react';
import { useEditSession } from '@/lib/editSession';

// RenderSpinner: a tiny status-bar note while an edit preview render is in
// flight (not a shared task — request-scoped).
export function RenderSpinner() {
  const rendering = useEditSession((s) => s.rendering);
  if (rendering === 0) return null;
  return (
    <span className="flex items-center gap-1.5 text-muted-foreground">
      <Loader2 className="size-3 animate-spin" />
      Rendering preview…
    </span>
  );
}
