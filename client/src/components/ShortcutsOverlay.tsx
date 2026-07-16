import { cn } from '@/lib/utils';
import { useUIStore } from '@/stores/uiStore';

// The handoff "KEYBOARD" plate as an in-app reference, on ? (Shift+/).
// `wide` cards span the grid and lay their rows out in two columns — used
// for the develop-control key map, which is long but shallow.
const CARDS: { title: string; rows: [string, string][]; wide?: boolean }[] = [
  {
    title: 'Navigate',
    rows: [
      ['Move', '← ↑ → ↓'],
      ['Extend (grid)', '⇧+arrow'],
      ['Select all', 'Ctrl+A'],
    ],
  },
  {
    title: 'Rate & flag',
    rows: [
      ['Rate', '1–5 · 0'],
      ['Pick / exclude', 'P · X'],
      ['Best of burst (pick / keep)', '⇧P · ⇧X'],
      ['Unflag', 'U'],
      ['Undo / redo', 'Ctrl+Z · Ctrl+⇧Z'],
    ],
  },
  {
    title: 'View & modes',
    rows: [
      ['Mode forward / back', 'Enter · Esc'],
      ['Contact sheet', 'G'],
      ['Zoom 1:1 / fit', 'Z · Space'],
      ['Pan (loupe)', '⇧+arrows'],
    ],
  },
  {
    title: 'Develop',
    rows: [
      ['Switch panel tab', 'Tab'],
      ['Prev / next control', '↑ / ↓'],
      ['Adjust', '+ / − · ⇧'],
      ['Release slider', 'Esc'],
      ['Copy / paste / crop', 'Ctrl+C Ctrl+V R'],
      ['Heal / spot removal', 'Q'],
      ['Visualize spots (healing)', 'A'],
      ['Spot opacity (spot selected)', '1–9 · 0'],
      ['Reset all', 'Ctrl+0'],
      ['Auto tone / colours / all', 'Ctrl+U · +⇧ · +Alt'],
      ['Auto presets', 'Ctrl+1–9'],
      ['My presets', 'Ctrl+⇧+1–9'],
    ],
  },
  // Mirrors CONTROL_KEYS in lib/keyboard.ts — press to focus, +/- to adjust.
  {
    title: 'Develop controls',
    wide: true,
    rows: [
      ['Exposure', 'E'],
      ['Brightness', 'B'],
      ['Gamma', 'G'],
      ['Shadow slope', 'S'],
      ['Contrast', 'C'],
      ['Saturation', 'A'],
      ['Vibrance', 'V'],
      ['Vignette', 'O'],
      ['WB mode', 'W'],
      ['Temperature', 'T'],
      ['Tint', 'I'],
      ['Kelvin', 'K'],
      ['Highlight recovery', 'H'],
      ['Noise reduction', 'N'],
      ['Median passes', 'M'],
      ['Demosaic', 'D'],
    ],
  },
];

export function ShortcutsOverlay() {
  const open = useUIStore((s) => s.shortcutsOpen);
  const setOpen = useUIStore((s) => s.setShortcutsOpen);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50" role="dialog" aria-label="Keyboard shortcuts">
      <div className="absolute inset-0 bg-black/40 backdrop-blur-[2px]" onClick={() => setOpen(false)} />
      <div className="absolute top-1/2 left-1/2 w-[720px] max-w-[90vw] -translate-x-1/2 -translate-y-1/2">
        <div className="mb-3 flex items-baseline gap-3">
          <span className="font-mono text-xs text-primary">KEYBOARD</span>
          <span className="text-lg font-semibold text-white drop-shadow">Muscle memory, preserved</span>
          <span className="ml-auto font-mono text-[11px] text-white/70">? or Esc to close</span>
        </div>
        <div className="grid grid-cols-2 gap-4">
          {CARDS.map((card) => (
            <div
              key={card.title}
              className={cn(
                'gap-[9px] rounded-xl border border-glass-border bg-card px-[18px] py-4 shadow-[0_30px_70px_-20px_rgba(0,0,0,.7)]',
                card.wide ? 'col-span-2 grid grid-cols-2 gap-x-10' : 'flex flex-col',
              )}
            >
              <div
                className={cn(
                  'mb-0.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase',
                  card.wide && 'col-span-2',
                )}
              >
                {card.title}
              </div>
              {card.rows.map(([label, keys]) => (
                <div key={label} className="flex justify-between text-[12.5px] text-secondary-foreground">
                  <span>{label}</span>
                  <span className="font-mono text-foreground">{keys}</span>
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
