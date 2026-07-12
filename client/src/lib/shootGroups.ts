// Sort and time-grouping of the library rail's shoots, computed client-side
// from Shoot.earliestTakenAt (the server keeps serving name order). Group ids
// are locale-independent so railGroups collapse keys survive locale changes;
// only the labels localize.
import type { Shoot } from '@/api/library';
import type { ShootGroup, ShootSort } from '@/stores/uiStore';

export interface ShootTimeGroup {
  /** Locale-independent bucket id: '2026' / '2026-03' / '2026-03-15' / 'no-date'. */
  id: string;
  label: string;
  /** The bucket's calendar year; null for the no-date bucket. */
  year: number | null;
  shoots: Shoot[];
}

const byNameAsc = (a: Shoot, b: Shoot) => {
  const an = a.name.toLowerCase();
  const bn = b.name.toLowerCase();
  return an < bn ? -1 : an > bn ? 1 : 0;
};

/**
 * Orders shoots for the rail. The parent's own loose-RAW row stays pinned
 * first; undated folders (earliestTakenAt 0, metadata not read yet or absent)
 * sort last in both date directions, among themselves by name.
 */
export function sortShoots(shoots: Shoot[], sort: ShootSort): Shoot[] {
  const cmp = (a: Shoot, b: Shoot): number => {
    if (a.isSelf !== b.isSelf) return a.isSelf ? -1 : 1;
    switch (sort) {
      case 'nameDesc':
        return -byNameAsc(a, b);
      case 'dateAsc':
      case 'dateDesc': {
        if (a.earliestTakenAt === 0 || b.earliestTakenAt === 0) {
          if (a.earliestTakenAt === b.earliestTakenAt) return byNameAsc(a, b);
          return a.earliestTakenAt === 0 ? 1 : -1;
        }
        const d = a.earliestTakenAt - b.earliestTakenAt;
        if (d !== 0) return sort === 'dateAsc' ? d : -d;
        return byNameAsc(a, b);
      }
      default:
        return byNameAsc(a, b);
    }
  };
  return [...shoots].sort(cmp);
}

const pad = (n: number) => String(n).padStart(2, '0');

/** Local-time bucket id for a capture time at the given granularity. */
export function shootGroupId(unixSec: number, g: ShootGroup): string {
  if (unixSec === 0) return 'no-date';
  const d = new Date(unixSec * 1000);
  if (g === 'year') return String(d.getFullYear());
  if (g === 'month') return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

const monthFmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' });
const dayFmt = new Intl.DateTimeFormat(undefined, {
  weekday: 'short',
  day: 'numeric',
  month: 'short',
  year: 'numeric',
});

function groupLabel(unixSec: number, g: ShootGroup): string {
  if (unixSec === 0) return 'No date';
  const d = new Date(unixSec * 1000);
  if (g === 'year') return String(d.getFullYear());
  if (g === 'month') return monthFmt.format(d);
  return dayFmt.format(d);
}

/**
 * Buckets already-sorted shoots by capture time. Group order follows the
 * shoot order except no-date, which always renders last; the isSelf row is
 * excluded — it renders above the groups as loose files, not a dated shoot.
 */
export function groupShoots(sorted: Shoot[], g: ShootGroup): ShootTimeGroup[] {
  const groups: ShootTimeGroup[] = [];
  const byId = new Map<string, ShootTimeGroup>();
  let noDate: ShootTimeGroup | null = null;
  for (const s of sorted) {
    if (s.isSelf) continue;
    const id = shootGroupId(s.earliestTakenAt, g);
    let group: ShootTimeGroup | null | undefined = id === 'no-date' ? noDate : byId.get(id);
    if (!group) {
      group = {
        id,
        label: groupLabel(s.earliestTakenAt, g),
        year: s.earliestTakenAt === 0 ? null : new Date(s.earliestTakenAt * 1000).getFullYear(),
        shoots: [],
      };
      if (id === 'no-date') noDate = group;
      else {
        byId.set(id, group);
        groups.push(group);
      }
    }
    group.shoots.push(s);
  }
  if (noDate) groups.push(noDate);
  return groups;
}

/**
 * railGroups collapse key for one time group under one managed parent.
 * Namespaced beside the parent's own key (parentKey from lib/library.ts).
 */
export function timeGroupKey(parentKeyStr: string, groupId: string): string {
  return `${parentKeyStr}|tg:${groupId}`;
}
