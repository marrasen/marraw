import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// uniqueName appends " (2)", " (3)", … until the name is free. With
// `keepBase` the base name itself is used when free (imports keep their
// names unless taken); without it numbering always starts (duplicates).
export function uniqueName(base: string, taken: { name: string }[], keepBase = false): string {
  const names = new Set(taken.map((t) => t.name));
  if (keepBase && !names.has(base)) return base;
  const stripped = base.replace(/ \(\d+\)$/, '');
  for (let n = 2; ; n++) {
    const candidate = `${stripped} (${n})`;
    if (!names.has(candidate)) return candidate;
  }
}

// clamp/clamp01 lived under six names across the client — `clamp`,
// `clamp01`, `clampUnit`, and inline Math.min(Math.max(…)) — which is
// harmless individually and means nobody finds the one that exists.
export const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));

/** Clamp to 0..1, the range normalized image coordinates and amounts live in. */
export const clamp01 = (n: number) => clamp(n, 0, 1);
