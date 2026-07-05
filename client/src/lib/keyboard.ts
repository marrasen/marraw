import { useEffect } from 'react';
import type { FlagType } from '@/api/library';
import { pasteEditParams, getEditParams } from '@/api/edits';
import { useApiClient } from '@/api/client';
import { toast } from 'sonner';
import { applyRating as doRating, applyFlag as doFlag } from '@/lib/actions';
import { useUIStore, selectionOrFocus } from '@/stores/uiStore';

// useKeyboard installs the app-wide culling keymap:
//   arrows      navigate (Shift extends the selection)
//   1-5 / 0     set / clear rating
//   P / X / U   pick / exclude / unflag
//   Enter or E  loupe view, Esc or G grid view
//   Ctrl+A      select all, Ctrl+C/V copy/paste edit settings
//   Ctrl+E      export dialog
export function useKeyboard() {
  const client = useApiClient();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t.isContentEditable) return;
      const s = useUIStore.getState();
      if (s.exportOpen) return;

      const move = (delta: number) => {
        const ids = s.visibleIds;
        if (ids.length === 0) return;
        const cur = s.focusId != null ? ids.indexOf(s.focusId) : -1;
        const next = cur < 0 ? 0 : Math.min(ids.length - 1, Math.max(0, cur + delta));
        s.focus(ids[next], { extend: e.shiftKey });
      };

      const applyRating = (n: number) => doRating(client, selectionOrFocus(), n);
      const applyFlag = (flag: FlagType) => doFlag(client, selectionOrFocus(), flag);

      const zoomStep = (factor: number) => {
        if (s.view !== 'loupe') return;
        // Stepping from 'fit' starts at 100%; the loupe resolves 'fit' itself.
        const cur = s.loupeZoom === 'fit' ? 1 : s.loupeZoom;
        s.setLoupeZoom(cur * factor);
      };

      if (e.ctrlKey || e.metaKey) {
        switch (e.key.toLowerCase()) {
          case 'a':
            e.preventDefault();
            s.selectAll(s.visibleIds);
            return;
          case 'c': {
            if (s.focusId == null) return;
            e.preventDefault();
            getEditParams(client, s.focusId).then((p) => {
              s.setClipboard(p ?? null);
              toast.success(p ? 'Edit settings copied' : 'No edits on this photo');
            });
            return;
          }
          case 'v': {
            const ids = selectionOrFocus();
            if (!s.clipboard || ids.length === 0) return;
            e.preventDefault();
            pasteEditParams(client, ids, s.clipboard)
              .then(() => toast.success(`Edit settings pasted to ${ids.length} photo${ids.length > 1 ? 's' : ''}`))
              .catch((err) => toast.error(`Paste failed: ${err.message}`));
            return;
          }
          case 'e':
            e.preventDefault();
            s.setExportOpen(true);
            return;
        }
        return;
      }

      switch (e.key) {
        case 'ArrowLeft':
          e.preventDefault();
          move(-1);
          break;
        case 'ArrowRight':
          e.preventDefault();
          move(1);
          break;
        case 'ArrowUp':
          e.preventDefault();
          move(s.view === 'grid' ? -s.gridCols : -1);
          break;
        case 'ArrowDown':
          e.preventDefault();
          move(s.view === 'grid' ? s.gridCols : 1);
          break;
        case '0':
        case '1':
        case '2':
        case '3':
        case '4':
        case '5':
          applyRating(Number(e.key));
          break;
        case 'x':
        case 'X':
          applyFlag('exclude');
          break;
        case 'p':
        case 'P':
          applyFlag('pick');
          break;
        case 'u':
        case 'U':
          applyFlag('none');
          break;
        case 'Enter':
        case 'e':
        case 'E':
          if (s.focusId != null) s.setView('loupe');
          break;
        case 'Escape':
        case 'g':
        case 'G':
          s.setView('grid');
          break;
        case '+':
        case '=':
          zoomStep(1.25);
          break;
        case '-':
          zoomStep(0.8);
          break;
        case 'z':
        case 'Z':
          if (s.view === 'loupe') s.setLoupeZoom(s.loupeZoom === 'fit' ? 1 : 'fit');
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [client]);
}
