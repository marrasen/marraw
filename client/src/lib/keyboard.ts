import { useEffect } from 'react';
import type { FlagType } from '@/api/library';
import { getEditParams } from '@/api/edits';
import { useApiClient } from '@/api/client';
import { toast } from 'sonner';
import { applyRating as doRating, applyFlag as doFlag } from '@/lib/actions';
import { useUIStore, selectionOrFocus } from '@/stores/uiStore';
import {
  esApplyAutoPreset,
  esApplyParams,
  esAuto,
  esMoveActive,
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
//   arrows        navigate (Shift extends the selection; pans in a loupe);
//                 in a develop loupe ↑/↓ focus the previous/next control
//   1-5 / 0       set / clear rating
//   P / X / U     pick / exclude / unflag
//   Enter · Esc   forward / back a mode: Library ⇄ Cull ⇄ Develop
//   E B W T I K G S C A V O H N M D   focus an edit control, +/- adjusts (Shift = big steps)
//   Ctrl+↑/↓      focus the previous/next develop control (alias of plain ↑/↓)
//   +/- / Z / Space   zoom (loupe, no control focused; Z/Space toggle 1:1↔fit)
//   Ctrl+A/C/V    select all, copy/paste edit settings
//   Ctrl+Z/Y      per-photo edit undo/redo
//   Ctrl+E        export dialog
//   Ctrl+U        auto dynamics (+Shift = auto colours, +Alt = auto everything)
//   Ctrl+1..9     creative auto presets (Settings → Auto presets)
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
      // P/X toggle: pressing the key of the flag the focused photo already
      // carries clears it (for the whole selection).
      const applyFlag = (flag: FlagType) => {
        const cur = s.focusId != null ? s.photoFlags.get(s.focusId) : undefined;
        doFlag(client, selectionOrFocus(), flag !== 'none' && cur === flag ? 'none' : flag);
      };

      const zoomStep = (factor: number) => {
        if (s.view !== 'loupe') return;
        // Stepping from 'fit' starts at the actual fit scale (mirrored out
        // by the loupe), so + walks out of fit instead of jumping to 1:1.
        const cur = s.loupeZoom === 'fit' ? s.loupeFitScale : s.loupeZoom;
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
          case 'k':
            e.preventDefault();
            s.setPaletteOpen(!s.paletteOpen);
            return;
          case 'u': {
            if (!es.draft) return;
            e.preventDefault();
            void esAuto(client, e.altKey ? ['all'] : e.shiftKey ? ['wb', 'color'] : ['tone']);
            return;
          }
          case '1': case '2': case '3': case '4': case '5':
          case '6': case '7': case '8': case '9': {
            const preset = s.autoPresets[Number(e.key) - 1];
            if (!preset || !es.draft) return;
            e.preventDefault();
            void esApplyAutoPreset(client, preset);
            return;
          }
          // Ctrl+↑/↓ walk the develop controls in panel order.
          case 'arrowup':
          case 'arrowdown': {
            if (!es.draft || s.mode === 'cull') return;
            e.preventDefault();
            esMoveActive(e.key === 'ArrowDown' ? 1 : -1);
            return;
          }
        }
        return;
      }

      // R toggles crop mode (needs a cinema surface, where the overlay
      // lives) — entering from Library switches to Develop for real, so the
      // mode tabs stay truthful.
      if (e.key.toLowerCase() === 'r' && es.draft) {
        e.preventDefault();
        if (!es.cropping && s.mode === 'library') s.setMode('develop');
        esSetCropping(client, !es.cropping);
        return;
      }

      const key = e.key.toLowerCase();

      // F11 = true fullscreen: even marraw's own chrome goes away.
      if (e.key === 'F11') {
        e.preventDefault();
        window.win?.toggleFullScreen();
        return;
      }

      // ? toggles the keyboard-shortcuts reference.
      if (e.key === '?') {
        e.preventDefault();
        s.setShortcutsOpen(!s.shortcutsOpen);
        return;
      }

      // G in Cull blows the scrubber into the contact sheet (elsewhere G
      // stays the Gamma control hotkey).
      if (key === 'g' && s.mode === 'cull') {
        e.preventDefault();
        s.setContactSheet(!s.contactSheet);
        return;
      }

      if (CONTROL_KEYS[key] && es.draft && s.mode !== 'cull') {
        e.preventDefault();
        esSetActive(client, CONTROL_KEYS[key]);
        return;
      }

      // Shift+arrows pan the loupe image (the grid and the contact sheet
      // keep Shift+arrow selection extension).
      if (e.shiftKey && s.view === 'loupe' && !s.contactSheet && e.key.startsWith('Arrow')) {
        e.preventDefault();
        const p = 0.1; // viewport fraction per press; key repeat makes it glide
        s.nudgeLoupePan(
          e.key === 'ArrowLeft' ? -p : e.key === 'ArrowRight' ? p : 0,
          e.key === 'ArrowUp' ? -p : e.key === 'ArrowDown' ? p : 0,
        );
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
        // In a develop loupe ↑/↓ walk the controls instead: photo-nav there is
        // redundant with ←/→ (rowStep is 1), and the grid/contact sheet keep
        // their row navigation. Same gate as the control-letter hotkeys.
        case 'ArrowUp':
          e.preventDefault();
          if (es.draft && s.mode !== 'cull' && s.view === 'loupe') esMoveActive(-1);
          else move(-rowStep);
          break;
        case 'ArrowDown':
          e.preventDefault();
          if (es.draft && s.mode !== 'cull' && s.view === 'loupe') esMoveActive(1);
          else move(rowStep);
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
          else if (s.mode === 'cull') s.setMode('develop');
          else if (s.mode === 'develop') s.setMode('cull');
          else if (s.focusId != null) s.setMode('cull');
          break;
        case 'Escape':
          if (s.fullscreen) {
            window.win?.toggleFullScreen();
          } else if (s.shortcutsOpen) {
            s.setShortcutsOpen(false);
          } else if (es.cropping) {
            esSetCropping(client, false);
          } else if (es.wbPicking) {
            esSetWBPicking(false);
          } else if (es.activeControl != null) {
            esSetActive(client, null);
          } else if (s.contactSheet) {
            s.setContactSheet(false);
          } else if (s.mode === 'library' && s.view === 'grid' && s.selection.size > 1) {
            s.clearSelection(); // "Esc to clear" on the batch selection bar
          } else if (s.mode === 'develop') {
            s.setMode('cull'); // step back one mode; Esc again reaches Library
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
