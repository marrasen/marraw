import { useUIStore } from '@/stores/uiStore';

// The handoff "KEYBOARD" plate as an in-app reference, on ? (Shift+/).
const CARDS: { title: string; rows: [string, string][] }[] = [
  {
    title: 'Navigate',
    rows: [
      ['Move', '← ↑ → ↓'],
      ['Extend', '⇧+arrow'],
      ['Select all', 'Ctrl+A'],
    ],
  },
  {
    title: 'Rate & flag',
    rows: [
      ['Rate', '1–5 · 0'],
      ['Pick / exclude', 'P · X'],
      ['Unflag', 'U'],
    ],
  },
  {
    title: 'View & modes',
    rows: [
      ['Cull loupe / back', 'Enter · Esc'],
      ['Contact sheet', 'G'],
      ['Zoom 1:1 / fit', 'Z · Space'],
    ],
  },
  {
    title: 'Develop',
    rows: [
      ['Focus a control', 'E C T H S…'],
      ['Adjust', '+ / − · ⇧'],
      ['Copy / paste / crop', 'Ctrl+C Ctrl+V R'],
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
              className="flex flex-col gap-[9px] rounded-xl border border-glass-border bg-card px-[18px] py-4 shadow-[0_30px_70px_-20px_rgba(0,0,0,.7)]"
            >
              <div className="mb-0.5 text-[10px] tracking-[.06em] text-muted-foreground uppercase">
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
