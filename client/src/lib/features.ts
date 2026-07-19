// Feature toggles (Settings → Features): whole features users can switch off
// to declutter the UI. The server stores only explicit overrides in a
// features map (uiStore mirror); this registry owns the ids, labels, and
// defaults, so absence of a key means a feature's default — including
// experimental features that default off until opted into.
import { useUIStore } from '@/stores/uiStore';

export type FeatureGroup = 'culling' | 'experimental';

export interface FeatureDef {
  label: string;
  description: string;
  group: FeatureGroup;
  experimental?: boolean;
  defaultOn: boolean;
}

export const FEATURES = {
  bursts: {
    label: 'Burst grouping',
    description:
      'Group near-duplicate frames into bursts: the Bursts and Auto-judge filter buttons, burst badges, and the best-of-burst shortcuts.',
    group: 'culling',
    defaultOn: true,
  },
  softFilter: {
    label: 'Soft-focus filter',
    description: 'Flag soft frames: the Soft filter button and the grid softness badge.',
    group: 'culling',
    defaultOn: true,
  },
  eyes: {
    label: 'Closed-eye detection',
    description:
      'Scan portraits for closed eyes: the Eyes scan, the Blinks filter button, and blink badges.',
    group: 'culling',
    defaultOn: true,
  },
  subjects: {
    label: 'Subject-aware focus',
    description:
      'Re-score sharpness on the detected subject instead of the whole frame. Scores already computed keep informing burst ranking.',
    group: 'culling',
    defaultOn: true,
  },
  suggestions: {
    label: 'ML edit suggestions',
    description:
      'Scene-aware suggested looks in the Presets panel, computed per photo by a local model.',
    group: 'experimental',
    experimental: true,
    defaultOn: false,
  },
} as const satisfies Record<string, FeatureDef>;

export type FeatureId = keyof typeof FEATURES;

export const FEATURE_IDS = Object.keys(FEATURES) as FeatureId[];

// Settings → Features section order.
export const FEATURE_GROUPS: { key: FeatureGroup; label: string }[] = [
  { key: 'culling', label: 'Culling aids' },
  { key: 'experimental', label: 'Experimental' },
];

// Effective state: the user's explicit override, else the registry default.
export function resolveFeature(overrides: Record<string, boolean>, id: FeatureId): boolean {
  return overrides[id] ?? FEATURES[id].defaultOn;
}

// Reactive read for components.
export function useFeature(id: FeatureId): boolean {
  return useUIStore((s) => resolveFeature(s.features, id));
}

// Snapshot read for non-React sites (keyboard handlers, actions).
export function featureEnabled(id: FeatureId): boolean {
  return resolveFeature(useUIStore.getState().features, id);
}
