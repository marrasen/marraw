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
