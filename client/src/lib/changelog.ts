// The repo-root CHANGELOG.md, inlined into the bundle at build time so the
// packaged file:// app carries its own release notes. Only two constructs
// are recognized: `## X.Y.Z - YYYY-MM-DD` version headers and `-` bullets;
// everything else (title, prose, blank lines) is ignored.
import changelogRaw from '../../../CHANGELOG.md?raw';

export interface ChangelogEntry {
  version: string;
  date: string;
  items: string[];
}

const versionHeader = /^##\s+v?(\d+\.\d+\.\d+)(?:\s*[-–]\s*(.+))?\s*$/;
const bullet = /^[-*]\s+(.+)$/;

export function parseChangelog(raw: string = changelogRaw): ChangelogEntry[] {
  const entries: ChangelogEntry[] = [];
  let open: ChangelogEntry | null = null;
  for (const line of raw.split(/\r?\n/)) {
    const header = versionHeader.exec(line);
    if (header) {
      open = { version: header[1], date: header[2]?.trim() ?? '', items: [] };
      entries.push(open);
      continue;
    }
    const item = bullet.exec(line);
    if (item && open) open.items.push(item[1].trim());
  }
  return entries.filter((e) => e.items.length > 0);
}

// Plain 3-part numeric compare — the app versions are simple X.Y.Z, no need
// for a semver dependency. Missing or non-numeric parts compare as 0.
export function compareVersions(a: string, b: string): number {
  const pa = a.split('.').map(Number);
  const pb = b.split('.').map(Number);
  for (let i = 0; i < 3; i++) {
    const d = (pa[i] || 0) - (pb[i] || 0);
    if (d !== 0) return d;
  }
  return 0;
}

// Entries in (lastSeen, current], newest first. '' lastSeen means a fresh
// install: show nothing, the caller baselines silently. A downgrade
// (lastSeen > current) naturally yields nothing too.
export function entriesSince(
  lastSeen: string,
  current: string,
  entries: ChangelogEntry[] = parseChangelog(),
): ChangelogEntry[] {
  if (lastSeen === '') return [];
  return entries
    .filter(
      (e) =>
        compareVersions(e.version, lastSeen) > 0 && compareVersions(e.version, current) <= 0,
    )
    .sort((a, b) => compareVersions(b.version, a.version));
}
