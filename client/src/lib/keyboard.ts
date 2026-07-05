import { useEffect } from 'react';
import type { FlagType } from '@/api/library';
import { getEditParams } from '@/api/edits';
import { useApiClient } from '@/api/client';
import { toast } from 'sonner';
import { applyRating as doRating, applyFlag as doFlag } from '@/lib/actions';
import { useUIStore, selectionOrFocus } from '@/stores/uiStore';
import {
  esApplyParams,
  esRedo,
  esSetActive,
  esSetWBPicking,
  esStep,
  esUndo,
  useEditSession,
  type ControlId,
} from '@/lib/editSession';

// Keys that focus an edit control; +/- then adjusts it, Esc returns to the
// image (where +/- zooms again).
const CONTROL_KEYS: Record<string, ControlId> = {
  e: 'expEV',
  b: 'bright',
  w: 'wbMode',
  t: 'wbTemp',
  i: 'wbTint',
  g: 'gamma',
  s: 'shadow',
  h: 'highlight',
  n: 'nrThreshold',
  m: 'medPasses',
};

// useKeyboard installs the app-wide keymap:
//   arrows        navigate (Shift extends the selection)
//   1-5 / 0       set / clear rating
//   P / X / U     pick / exclude / unflag
//   Enter         loupe view · Esc control → grid
//   E B W T I G S H N M   focus an edit control, +/- adjusts (Shift = big steps)
//   +/- / Z / Space   zoom (loupe, no control focused; Z/Space toggle 1:1↔fit)
//   Ctrl+A/C/V    select all, copy/paste edit settings
//   Ctrl+Z/Y      per-photo edit undo/redo
//   Ctrl+E        export dialog
export function useKeyboard() {
  const client = useApiClient();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t.isContentEditable) return;
      const s = useUIStore.getState();
      if (s.exportOpen) return;
      const es = useEditSession.getState();

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
            if (!s.clipboard || s.focusId == null) return;
            e.preventDefault();
            esApplyParams(client, s.clipboard);
            toast.success('Edit settings pasted');
            return;
          }
          case 'z':
            e.preventDefault();
            if (e.shiftKey) esRedo(client);
            else esUndo(client);
            return;
          case 'y':
            e.preventDefault();
            esRedo(client);
            return;
          case 'e':
            e.preventDefault();
            s.setExportOpen(true);
            return;
        }
        return;
      }

      const key = e.key.toLowerCase();
      if (CONTROL_KEYS[key] && es.draft) {
        e.preventDefault();
        esSetActive(CONTROL_KEYS[key]);
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
          if (s.focusId != null) s.setView('loupe');
          break;
        case 'Escape':
          if (es.wbPicking) {
            esSetWBPicking(false);
          } else if (es.activeControl != null) {
            esSetActive(null);
          } else {
            s.setView('grid');
          }
          break;
        case '+':
        case '=':
          if (es.activeControl != null) esStep(client, es.activeControl, 1, e.shiftKey);
          else zoomStep(1.25);
          break;
        case '-':
        case '_':
          if (es.activeControl != null) esStep(client, es.activeControl, -1, e.shiftKey);
          else zoomStep(0.8);
          break;
        case 'z':
        case 'Z':
        case ' ':
          if (s.view === 'loupe') {
            e.preventDefault(); // space must not scroll or trigger a focused button
            s.setLoupeZoom(s.loupeZoom === 'fit' ? 1 : 'fit');
          }
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [client]);
}
