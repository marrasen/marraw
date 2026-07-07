import { useEffect, useMemo, useRef, useState } from 'react';
import { Search } from 'lucide-react';
import { useApiClient, type ApiClient } from '@/api/client';
import { cn } from '@/lib/utils';
import {
  esApplyAutoPreset,
  esAuto,
  esSetActive,
  esSetCropping,
  esSetWBPicking,
  type ControlId,
} from '@/lib/editSession';
import type { AutoPreset } from '@/lib/autoPresets';
import { useTheme } from '@/components/theme-provider';
import { useUIStore } from '@/stores/uiStore';

interface Command {
  id: string;
  label: string;
  group: string;
  hint?: string;
  run: (client: ApiClient) => void;
}

// Develop controls reachable by name — jumping switches to Develop and
// focuses the control so +/- adjust immediately.
const CONTROLS: { id: ControlId; label: string; hint?: string }[] = [
  { id: 'expEV', label: 'Exposure', hint: 'E' },
  { id: 'bright', label: 'Brightness', hint: 'B' },
  { id: 'gamma', label: 'Gamma', hint: 'G' },
  { id: 'shadow', label: 'Shadow slope', hint: 'S' },
  { id: 'contrast', label: 'Contrast', hint: 'C' },
  { id: 'wbMode', label: 'White balance mode', hint: 'W' },
  { id: 'wbTemp', label: 'Temperature', hint: 'T' },
  { id: 'wbTint', label: 'Tint', hint: 'I' },
  { id: 'wbKelvin', label: 'Kelvin', hint: 'K' },
  { id: 'saturation', label: 'Saturation', hint: 'A' },
  { id: 'vibrance', label: 'Vibrance', hint: 'V' },
  { id: 'vignette', label: 'Vignette', hint: 'O' },
  { id: 'highlight', label: 'Highlight recovery', hint: 'H' },
  { id: 'nrThreshold', label: 'Noise reduction', hint: 'N' },
  { id: 'medPasses', label: 'Median passes', hint: 'M' },
  { id: 'demosaic', label: 'Demosaic', hint: 'D' },
];

function buildCommands(
  hasFolder: boolean,
  autoPresets: AutoPreset[],
  setTheme: (t: 'dark' | 'light' | 'system') => void,
): Command[] {
  const ui = () => useUIStore.getState();
  const out: Command[] = [
    { id: 'mode-library', label: 'Go to Library', group: 'Modes', run: () => ui().setMode('library') },
  ];
  if (hasFolder) {
    out.push(
      { id: 'mode-cull', label: 'Go to Cull', group: 'Modes', run: () => ui().setMode('cull') },
      { id: 'mode-develop', label: 'Go to Develop', group: 'Modes', run: () => ui().setMode('develop') },
      { id: 'sheet', label: 'Contact sheet', group: 'Modes', hint: 'G', run: () => {
          ui().setMode('cull');
          ui().setContactSheet(true);
        } },
      { id: 'export', label: 'Export…', group: 'Actions', hint: 'Ctrl+E', run: () => ui().setExportOpen(true) },
    );
  }
  out.push(
    { id: 'add-folder', label: 'Add folder to library…', group: 'Actions', run: () => ui().setAddFolderOpen(true) },
    { id: 'settings', label: 'Settings…', group: 'Actions', run: () => ui().setSettingsOpen(true) },
    { id: 'shortcuts', label: 'Keyboard shortcuts', group: 'Actions', hint: '?', run: () => ui().setShortcutsOpen(true) },
    // "D" toggles dark/light; these set an explicit theme by name.
    { id: 'theme-dark', label: 'Theme: Dark', group: 'Appearance', run: () => setTheme('dark') },
    { id: 'theme-light', label: 'Theme: Light', group: 'Appearance', run: () => setTheme('light') },
    { id: 'theme-system', label: 'Theme: Follow system', group: 'Appearance', run: () => setTheme('system') },
  );
  if (hasFolder) {
    out.push(
      { id: 'crop', label: 'Crop & straighten', group: 'Develop', hint: 'R', run: (client) => {
          ui().setMode('develop');
          esSetCropping(client, true);
        } },
      { id: 'wb-pick', label: 'White balance eyedropper', group: 'Develop', run: () => {
          ui().setMode('develop');
          esSetWBPicking(true);
        } },
      { id: 'auto-tone', label: 'Auto dynamics', group: 'Develop', hint: 'Ctrl+U', run: (client) => {
          ui().setMode('develop');
          void esAuto(client, ['tone']);
        } },
      { id: 'auto-color', label: 'Auto colours', group: 'Develop', hint: 'Ctrl+⇧+U', run: (client) => {
          ui().setMode('develop');
          void esAuto(client, ['wb', 'color']);
        } },
      { id: 'auto-all', label: 'Auto everything', group: 'Develop', hint: 'Ctrl+Alt+U', run: (client) => {
          ui().setMode('develop');
          void esAuto(client, ['all']);
        } },
      ...autoPresets.map((p, i) => ({
        id: `auto-preset-${p.id}`,
        label: p.name,
        group: 'Auto presets',
        hint: i < 9 ? `Ctrl+${i + 1}` : undefined,
        run: (client: ApiClient) => {
          ui().setMode('develop');
          void esApplyAutoPreset(client, p);
        },
      })),
      ...CONTROLS.map((c) => ({
        id: `ctl-${c.id}`,
        label: c.label,
        group: 'Develop',
        hint: c.hint,
        run: (client) => {
          ui().setMode('develop');
          esSetActive(client, c.id);
        },
      })),
    );
  }
  return out;
}

/**
 * The ⌘K palette: jump to any mode, control, or action by name.
 */
export function CommandPalette() {
  const client = useApiClient();
  const open = useUIStore((s) => s.paletteOpen);
  const setOpen = useUIStore((s) => s.setPaletteOpen);
  const hasFolder = useUIStore((s) => s.folderId != null);
  const autoPresets = useUIStore((s) => s.autoPresets);
  const { setTheme } = useTheme();
  const [query, setQuery] = useState('');
  const [index, setIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const commands = useMemo(
    () => buildCommands(hasFolder, autoPresets, setTheme),
    [hasFolder, autoPresets, setTheme],
  );
  const q = query.trim().toLowerCase();
  const matches = useMemo(
    () => (q ? commands.filter((c) => c.label.toLowerCase().includes(q)) : commands),
    [commands, q],
  );

  useEffect(() => {
    if (open) {
      setQuery('');
      setIndex(0);
      // The dialog mounts in this same tick; focus on the next frame.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);
  useEffect(() => setIndex(0), [q]);
  useEffect(() => {
    listRef.current
      ?.querySelector(`[data-cmd-index="${index}"]`)
      ?.scrollIntoView({ block: 'nearest' });
  }, [index]);

  if (!open) return null;

  const runCommand = (c: Command) => {
    setOpen(false);
    // Let the palette unmount before the command flips focus/mode.
    setTimeout(() => c.run(client), 0);
  };

  return (
    <div className="fixed inset-0 z-50" role="dialog" aria-label="Command palette">
      <div className="absolute inset-0 bg-black/40 backdrop-blur-[2px]" onClick={() => setOpen(false)} />
      <div className="absolute top-[18%] left-1/2 w-[520px] -translate-x-1/2 overflow-hidden rounded-[13px] border border-glass-border bg-popover/98 shadow-[0_50px_120px_-30px_rgba(0,0,0,.9)]">
        <div className="flex items-center gap-2.5 border-b px-4 py-3">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            ref={inputRef}
            className="w-full bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
            placeholder="Jump to any mode, control, or action…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault();
                setOpen(false);
              } else if (e.key === 'ArrowDown') {
                e.preventDefault();
                setIndex((i) => Math.min(matches.length - 1, i + 1));
              } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                setIndex((i) => Math.max(0, i - 1));
              } else if (e.key === 'Enter' && matches[index]) {
                e.preventDefault();
                runCommand(matches[index]);
              }
            }}
          />
          <span className="shrink-0 rounded bg-black/10 px-1.5 py-px font-mono text-[10.5px] text-muted-foreground dark:bg-white/10">
            esc
          </span>
        </div>
        <div ref={listRef} className="max-h-[320px] overflow-y-auto p-1.5">
          {matches.length === 0 && (
            <div className="px-3 py-4 text-sm text-muted-foreground">Nothing matches.</div>
          )}
          {matches.map((c, i) => {
            const firstOfGroup = i === 0 || matches[i - 1].group !== c.group;
            return (
              <div key={c.id}>
                {firstOfGroup && (
                  <div className="px-2.5 pt-2 pb-1 text-[10px] tracking-[.06em] text-faint uppercase">
                    {c.group}
                  </div>
                )}
                <button
                  data-cmd-index={i}
                  className={cn(
                    'flex h-8 w-full items-center gap-2.5 rounded-[7px] px-2.5 text-left text-[13px]',
                    i === index ? 'bg-sidebar-accent text-foreground' : 'text-secondary-foreground',
                  )}
                  onMouseEnter={() => setIndex(i)}
                  onClick={() => runCommand(c)}
                >
                  <span className="flex-1 truncate">{c.label}</span>
                  {c.hint && <span className="font-mono text-[10.5px] text-faint">{c.hint}</span>}
                </button>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
