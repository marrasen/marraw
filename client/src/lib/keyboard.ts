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
  esSetCropping,
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
  k: 'wbKelvin',
  g: 'gamma',
  s: 'shadow',
  c: 'contrast',
  a: 'saturation',
  v: 'vibrance',
  o: 'vignette',
  h: 'highlight',
  n: 'nrThreshold',
  m: 'medPasses',
  d: 'demosaic',
};

// useKeyboard installs the app-wide keymap:
//   arrows        navigate (Shift extends the selection)
//   1-5 / 0       set / clear rating
//   P / X / U     pick / exclude / unflag
//   Enter         loupe view · Esc control → grid
//   E B W T I K G S C A V O H N M D   focus an edit control, +/- adjusts (Shift = big steps)
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
      // Vertical arrows move by a row: the grid's live column count, the
      // contact sheet's fixed 8, one frame in a loupe.
      const rowStep =
        s.mode === 'cull' && s.contactSheet ? 8 : s.mode === 'library' && s.view === 'grid' ? s.gridCols : 1;

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

      // R toggles crop mode (needs a loupe surface, where the overlay lives).
      if (e.key.toLowerCase() === 'r' && es.draft) {
        e.preventDefault();
        if (!es.cropping && s.mode === 'library') s.setView('loupe');
        esSetCropping(client, !es.cropping);
        return;
      }

      const key = e.key.toLowerCase();

      // G in Cull blows the scrubber into the contact sheet (elsewhere G
      // stays the Gamma control hotkey).
      if (key === 'g' && s.mode === 'cull') {
        e.preventDefault();
        s.setContactSheet(!s.contactSheet);
        return;
      }

      if (CONTROL_KEYS[key] && es.draft && s.mode !== 'cull') {
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
          move(-rowStep);
          break;
        case 'ArrowDown':
          e.preventDefault();
          move(rowStep);
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
          // In crop mode, Enter applies the crop rather than switching mode.
          if (es.cropping) esSetCropping(client, false);
          else if (s.contactSheet) s.setContactSheet(false);
          else if (s.focusId != null && s.mode === 'library' && s.view === 'grid') s.setMode('cull');
          break;
        case 'Escape':
          if (es.cropping) {
            esSetCropping(client, false);
          } else if (es.wbPicking) {
            esSetWBPicking(false);
          } else if (es.activeControl != null) {
            esSetActive(null);
          } else if (s.contactSheet) {
            s.setContactSheet(false);
          } else {
            s.setMode('library');
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
