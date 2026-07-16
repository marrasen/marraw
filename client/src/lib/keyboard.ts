import { useEffect } from 'react';
import type { FlagType } from '@/api/library';
import { getEditParams } from '@/api/edits';
import { useApiClient } from '@/api/client';
import { toast } from 'sonner';
import { applyRating as doRating, applyFlag as doFlag, applyFlagOps } from '@/lib/actions';
import { chCanRedo, chCanUndo, chRedo, chUndo } from '@/lib/cullHistory';
import { rowNeighbor } from '@/lib/gridNav';
import { useUIStore, selectionOrFocus, type DevelopTab } from '@/stores/uiStore';
import {
  esApplyAutoPreset,
  esApplyParams,
  esAuto,
  esMoveActive,
  esMoveMaskActive,
  esRedo,
  esReset,
  esSetActive,
  esSetActiveMask,
  esSetActiveSpot,
  esSetCropping,
  esSetHealing,
  esSetWBPicking,
  esRemoveSpot,
  esStep,
  esStepMask,
  esUndo,
  esWBPickCancel,
  esWBPickDone,
  useEditSession,
  type ControlId,
} from '@/lib/editSession';

// Keys that focus an edit control; +/- then adjusts it, Esc returns to the
// image (where +/- zooms again).
export const CONTROL_KEYS: Record<string, ControlId> = {
  e: 'expEV',
  b: 'bright',
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
//                 (on the Local tab: the previous/next mask slider, walking
//                 across masks at the ends)
//   1-5 / 0       set / clear rating
//   P / X / U     pick / exclude / unflag
//   ⇧P / ⇧X       keep this burst frame, exclude the rest (⇧P also picks it)
//   Enter · Esc   forward / back a mode: Library ⇄ Cull ⇄ Develop
//   E B T I K G S C A V O H N M D   focus an edit control, +/- adjusts (Shift = big steps)
//   W             toggle the white-balance eyedropper (Enter keep · Esc cancel)
//   Ctrl+↑/↓      focus the previous/next develop control (alias of plain ↑/↓)
//   +/- / Z / Space   zoom (loupe, no control focused — Cull never focuses one;
//                 Z/Space toggle 1:1↔fit)
//   Tab           in Develop, cycle the Develop/Local/Presets/Info tabs (⇧ backward);
//                 elsewhere Tab is swallowed — native focus is useless here
//   Ctrl+A/C/V    select all, copy/paste edit settings
//   Ctrl+Z/Y      undo/redo — per-photo edits in Develop; flags & ratings in
//                 Library/Cull (falling back to edit history when empty)
//   Ctrl+0        reset all develop settings
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
      // The Local tab (value 'masks') retargets the slider keys: ↑/↓ walk the
      // mask sliders (across masks) and +/- step the focused one.
      const masksTab = s.developTab === 'masks' && s.mode !== 'cull' && !!es.draft;

      const move = (delta: number) => {
        const ids = s.visibleIds;
        if (ids.length === 0) return;
        const cur = s.focusId != null ? ids.indexOf(s.focusId) : -1;
        const next = cur < 0 ? 0 : Math.min(ids.length - 1, Math.max(0, cur + delta));
        s.focus(ids[next], { extend: e.shiftKey });
      };
      // Vertical arrows move by a row, resolved against the row model the
      // mounted grid published (navRowStarts: the flat index each visual row
      // begins at, with group boundaries and variable-width justified rows
      // already baked in). An empty model means a 1D surface (loupe/filmstrip),
      // where ↑/↓ is just a flat ±1 step.
      const moveRow = (dir: -1 | 1) => {
        const ids = s.visibleIds;
        if (ids.length === 0) return;
        if (s.navRowStarts.length === 0) {
          move(dir);
          return;
        }
        const cur = s.focusId != null ? ids.indexOf(s.focusId) : -1;
        if (cur < 0) {
          s.focus(ids[0], { extend: e.shiftKey });
          return;
        }
        const next = rowNeighbor(cur, ids.length, s.navRowStarts, dir, s.navColCenters ?? undefined);
        s.focus(ids[next], { extend: e.shiftKey });
      };

      const applyRating = (n: number) => doRating(client, selectionOrFocus(), n);
      // P/X toggle: pressing the key of the flag the focused photo already
      // carries clears it (for the whole selection).
      const applyFlag = (flag: FlagType) => {
        const cur = s.focusId != null ? s.photoMeta.get(s.focusId)?.flag : undefined;
        doFlag(client, selectionOrFocus(), flag !== 'none' && cur === flag ? 'none' : flag);
      };
      // Shift+P/X judge the focused photo's whole burst in one stroke: keep
      // this frame and exclude its near-duplicate siblings. P also picks the
      // kept frame; X leaves its flag untouched. No toggle semantics — a
      // second press is idempotent. Outside a burst there is nothing to
      // reject, so the keys deliberately no-op instead of guessing. The whole
      // judgement is one undo entry: a single Ctrl+Z restores the burst.
      const judgeBurst = (pickKept: boolean) => {
        if (s.focusId == null) return;
        const members = s.burstMembers.get(s.focusId);
        if (!members) return;
        const rest = members.filter((id) => id !== s.focusId);
        const ops: { ids: number[]; flag: FlagType }[] =
          rest.length > 0 ? [{ ids: rest, flag: 'exclude' }] : [];
        if (pickKept) ops.push({ ids: [s.focusId], flag: 'pick' });
        applyFlagOps(client, ops, 'Best of burst');
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
            esApplyParams(client, s.clipboard, { label: 'Paste' });
            toast.success('Edit settings pasted');
            return;
          }
          // Outside Develop, Ctrl+Z works the flag/rating history first and
          // falls back to the edit history when that stack is spent — the
          // develop dials and edit panel stay usable in Library/Cull, so
          // their undo must stay reachable there too.
          case 'z':
            e.preventDefault();
            if (e.shiftKey) {
              if (s.mode !== 'develop' && chCanRedo()) chRedo(client);
              else esRedo(client);
            } else {
              if (s.mode !== 'develop' && chCanUndo()) chUndo(client);
              else esUndo(client);
            }
            return;
          case 'y':
            e.preventDefault();
            if (s.mode !== 'develop' && chCanRedo()) chRedo(client);
            else esRedo(client);
            return;
          case 'e':
            e.preventDefault();
            s.setExportOpen(true);
            return;
          case '0':
            if (!es.draft) return;
            e.preventDefault();
            esReset(client);
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
          // Ctrl+↑/↓ walk the develop controls in panel order (the mask
          // sliders when the Masks tab is up).
          case 'arrowup':
          case 'arrowdown': {
            if (!es.draft || s.mode === 'cull') return;
            e.preventDefault();
            const dir = e.key === 'ArrowDown' ? 1 : -1;
            if (masksTab) esMoveMaskActive(dir);
            else esMoveActive(dir);
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

      // Q toggles the heal / spot-removal tool (like R for crop): its overlay
      // lives on the cinema surface, so entering from Library switches to
      // Develop for real. Its panel section lives on the Local tab — reveal
      // it so the spot list isn't hidden behind Develop/Presets/Info.
      if (e.key.toLowerCase() === 'q' && es.draft) {
        e.preventDefault();
        if (!es.healing) {
          if (s.mode === 'library') s.setMode('develop');
          s.setDevelopTab('masks');
        }
        esSetHealing(!es.healing);
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

      // Tab is swallowed everywhere: native focus traversal is useless here
      // because Enter and Space are bound to actions, so a tab-focused control
      // can't be activated. Jump to any control or tab via ⌘K instead. In
      // Develop, Tab still cycles the Develop/Local/Presets/Info panel tabs
      // (⇧ backward). Open dialogs keep their own focus trap.
      if (e.key === 'Tab') {
        if (s.settingsOpen || s.addFolderOpen || s.shortcutsOpen) return;
        e.preventDefault();
        if (s.mode === 'develop') {
          const order: DevelopTab[] = ['develop', 'masks', 'presets', 'info'];
          const i = order.indexOf(s.developTab);
          s.setDevelopTab(order[(i + (e.shiftKey ? order.length - 1 : 1)) % order.length]);
        }
        return;
      }

      // G in Cull blows the scrubber into the contact sheet (elsewhere G
      // stays the Gamma control hotkey).
      if (key === 'g' && s.mode === 'cull') {
        e.preventDefault();
        s.setContactSheet(!s.contactSheet);
        return;
      }

      // W toggles the white-balance eyedropper (like R for crop): opening it
      // starts a pick session, pressing W again keeps the previewed value.
      // Enter/Esc keep/cancel too (see the switch below).
      if (key === 'w' && es.draft && s.mode !== 'cull') {
        e.preventDefault();
        if (es.wbPicking) esWBPickDone(client);
        else esSetWBPicking(true);
        return;
      }

      if (CONTROL_KEYS[key] && es.draft && s.mode !== 'cull') {
        e.preventDefault();
        s.setDevelopTab('develop'); // reveal the slider if on the Presets/Info tab
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
        // redundant with ←/→ (a loupe row is one frame), and the grid/contact
        // sheet keep their row navigation. Same gate as the control-letter
        // hotkeys.
        case 'ArrowUp':
          e.preventDefault();
          if (masksTab && s.view === 'loupe') esMoveMaskActive(-1);
          else if (es.draft && s.mode !== 'cull' && s.view === 'loupe') esMoveActive(-1);
          else moveRow(-1);
          break;
        case 'ArrowDown':
          e.preventDefault();
          if (masksTab && s.view === 'loupe') esMoveMaskActive(1);
          else if (es.draft && s.mode !== 'cull' && s.view === 'loupe') esMoveActive(1);
          else moveRow(1);
          break;
        case '0':
        case '1':
        case '2':
        case '3':
        case '4':
        case '5':
          applyRating(Number(e.key));
          break;
        case 'Delete':
        case 'Backspace':
          // Delete the selected retouch spot while the heal tool is active.
          if (es.healing && es.activeSpot != null) {
            e.preventDefault();
            esRemoveSpot(client, es.activeSpot);
          }
          break;
        case 'x':
        case 'X':
          if (e.shiftKey) judgeBurst(false);
          else applyFlag('exclude');
          break;
        case 'p':
        case 'P':
          if (e.shiftKey) judgeBurst(true);
          else applyFlag('pick');
          break;
        case 'u':
        case 'U':
          applyFlag('none');
          break;
        case 'Enter':
          // In crop / WB-pick mode, Enter applies rather than switching mode.
          if (es.cropping) esSetCropping(client, false);
          else if (es.wbPicking) esWBPickDone(client);
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
            esWBPickCancel(client); // revert to the pre-picker draft
          } else if (es.healing) {
            // Drop a spot selection first, then exit the tool on a second Esc.
            if (es.activeSpot != null) esSetActiveSpot(null);
            else esSetHealing(false);
          } else if (es.activeMask != null || es.activeMaskControl != null) {
            esSetActiveMask(null); // drop mask selection + slider focus + overlay
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
          if (masksTab && es.activeMaskControl != null) esStepMask(client, 1, e.shiftKey);
          else if (!masksTab && es.activeControl != null) esStep(client, es.activeControl, 1, e.shiftKey);
          else zoomStep(1.25);
          break;
        case '-':
        case '_':
          if (masksTab && es.activeMaskControl != null) esStepMask(client, -1, e.shiftKey);
          else if (!masksTab && es.activeControl != null) esStep(client, es.activeControl, -1, e.shiftKey);
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
