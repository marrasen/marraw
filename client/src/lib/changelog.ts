// The repo-root CHANGELOG.md, inlined into the bundle at build time so the
// packaged file:// app carries its own release notes. Only two constructs
// are recognized: `## X.Y.Z - YYYY-MM-DD` version headers (the version may
// carry a prerelease suffix, `## X.Y.Z-beta.N - YYYY-MM-DD`) and `-` bullets;
// everything else (title, prose, blank lines) is ignored.
import changelogRaw from '../../../CHANGELOG.md?raw';

export interface ChangelogEntry {
  version: string;
  date: string;
  items: string[];
}

// The prerelease run has no spaces, so it can never swallow the ` - date`
// that follows it.
const versionHeader = /^##\s+v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)(?:\s*[-–]\s*(.+))?\s*$/;
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

// Semver precedence, hand-rolled (the app ships X.Y.Z and X.Y.Z-beta.N — not
// worth a dependency). Missing or non-numeric core parts compare as 0.
//
// The prerelease rules are the ones that matter here: a version WITH a
// prerelease sorts below the same version without one, so 0.10.0-beta.13 <
// 0.10.0 and the stable release's notes still count as news to a beta tester.
// Identifiers compare numerically when both are numeric (beta.9 < beta.13 —
// a plain string compare gets that backwards), otherwise lexically, and a
// longer run of identifiers wins a tie on the shared prefix.
export function compareVersions(a: string, b: string): number {
  const [coreA, preA = ''] = splitPrerelease(a);
  const [coreB, preB = ''] = splitPrerelease(b);
  const pa = coreA.split('.').map(Number);
  const pb = coreB.split('.').map(Number);
  for (let i = 0; i < 3; i++) {
    const d = (pa[i] || 0) - (pb[i] || 0);
    if (d !== 0) return d;
  }
  if (preA === preB) return 0;
  if (preA === '') return 1;
  if (preB === '') return -1;
  const ia = preA.split('.');
  const ib = preB.split('.');
  for (let i = 0; i < Math.max(ia.length, ib.length); i++) {
    if (ia[i] === undefined) return -1;
    if (ib[i] === undefined) return 1;
    if (ia[i] === ib[i]) continue;
    const na = /^\d+$/.test(ia[i]);
    const nb = /^\d+$/.test(ib[i]);
    if (na && nb) return Number(ia[i]) - Number(ib[i]);
    // Numeric identifiers always have lower precedence than alphanumeric.
    if (na !== nb) return na ? -1 : 1;
    return ia[i] < ib[i] ? -1 : 1;
  }
  return 0;
}

// "0.10.0-beta.13" -> ["0.10.0", "beta.13"]; build metadata (+sha) is dropped,
// it carries no precedence.
function splitPrerelease(v: string): [string, string?] {
  const core = v.split('+')[0];
  const dash = core.indexOf('-');
  return dash < 0 ? [core] : [core.slice(0, dash), core.slice(dash + 1)];
}

// Entries in (lastSeen, current], newest first. '' lastSeen means a fresh
// install: show nothing, the caller baselines silently. A downgrade
// (lastSeen > current) naturally yields nothing too.
//
// Prereleases fall out of the ordering: a beta tester moving beta.12 -> 13
// sees only beta.13, and moving off the last beta onto the stable release
// sees the stable section (which is the whole story of that version — the
// release skill folds the beta sections into it).
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
