// User-preset sanitization and the share-file format. Presets from the
// server arrive re-marshaled through edit.Params (complete, typed), so the
// read path mostly checks identity; presets from an IMPORTED FILE are
// untrusted JSON and every field is narrowed before it goes anywhere near
// the store. A leaf module (imports only the API types + catalogs).
import type { Mask, MaskAdjust, Params } from '@/api/edit';
import type { UserPreset } from '@/api/settings';
import { NEUTRAL } from '@/lib/controlSpecs';
import { PRESET_GROUPS } from '@/lib/presetSections';

const GROUP_IDS = new Set<string>(PRESET_GROUPS.map((g) => g.id));
const AUTO_SECTION_IDS = new Set(['tone', 'wb', 'color']);

// sanitizeUserPresets narrows a stored preset list to the client shape,
// dropping malformed entries — unknown section ids are filtered rather than
// rejected so the list survives older/newer builds (same contract as
// sanitizeAutoPresets). `trusted` marks server-round-tripped params (kept
// as-is); untrusted params (file import) are rebuilt field-by-field over
// NEUTRAL.
export function sanitizeUserPresets(raw: unknown, opts?: { trusted?: boolean }): UserPreset[] {
  if (!Array.isArray(raw)) return [];
  const out: UserPreset[] = [];
  for (const p of raw) {
    if (typeof p !== 'object' || p === null) continue;
    const { id, name, params, sections, relative, baseExpEV, autoSections } = p as Record<string, unknown>;
    if (typeof id !== 'string' || !id || typeof name !== 'string' || !name) continue;
    if (typeof params !== 'object' || params === null) continue;
    out.push({
      id,
      name,
      params: opts?.trusted ? (params as Params) : sanitizeParams(params as Record<string, unknown>),
      sections: sanitizeIds(sections, GROUP_IDS),
      relative: relative === true || undefined,
      baseExpEV: typeof baseExpEV === 'number' && Number.isFinite(baseExpEV) ? baseExpEV : undefined,
      autoSections: sanitizeIds(autoSections, AUTO_SECTION_IDS),
    });
  }
  return out;
}

function sanitizeIds(raw: unknown, known: Set<string>): string[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  const ids = raw.filter((s): s is string => typeof s === 'string' && known.has(s));
  return ids.length > 0 ? ids : undefined;
}

// sanitizeParams rebuilds untrusted params over NEUTRAL: only known keys,
// only matching primitive shapes, fixed-length numeric arrays checked
// element-wise. Value RANGES are the server's job (Normalize clamps on
// save); this guards shape so garbage can't reach the store or the wire.
// Painted masks and retouch spots never travel in a preset (local
// geometry); AI-mask RECIPES do (they re-run detection per photo) and are
// narrowed field-by-field.
function sanitizeParams(raw: Record<string, unknown>): Params {
  const out: Params = { ...NEUTRAL };
  const fields = out as unknown as Record<string, unknown>;
  for (const [key, neutralVal] of Object.entries(NEUTRAL)) {
    const v = raw[key];
    if (v === undefined) continue;
    if (Array.isArray(neutralVal)) {
      if (
        Array.isArray(v) &&
        v.length === neutralVal.length &&
        v.every((x) => typeof x === 'number' && Number.isFinite(x))
      ) {
        fields[key] = [...v];
      }
    } else if (typeof v === typeof neutralVal) {
      if (typeof v === 'number' && !Number.isFinite(v)) continue;
      fields[key] = v;
    }
  }
  out.masks = sanitizeMaskRecipes(raw.masks);
  return out;
}

const AI_KINDS = new Set(['subject', 'class', 'depth']);
const ADJUST_KEYS = [
  'expEV', 'contrast', 'toneHighlights', 'toneShadows', 'whites', 'blacks',
  'temp', 'tint', 'saturation',
] as const;

function finite(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

// sanitizeMaskRecipes keeps only AI-mask recipes from an imported preset:
// known kind, numeric tuning, numeric adjust — mapVer always cleared
// (applying re-runs detection and stamps the local model's version).
function sanitizeMaskRecipes(raw: unknown): Mask[] | undefined {
  if (!Array.isArray(raw)) return undefined;
  const out: Mask[] = [];
  for (const m of raw) {
    if (typeof m !== 'object' || m === null) continue;
    const r = m as Record<string, unknown>;
    if (r.type !== 'ai' || typeof r.aiKind !== 'string' || !AI_KINDS.has(r.aiKind)) continue;
    const adjust: MaskAdjust = {};
    if (typeof r.adjust === 'object' && r.adjust !== null) {
      for (const k of ADJUST_KEYS) {
        const v = finite((r.adjust as Record<string, unknown>)[k]);
        if (v !== undefined && v !== 0) adjust[k] = v;
      }
    }
    out.push({
      type: 'ai',
      aiKind: r.aiKind as Mask['aiKind'],
      mapVer: '',
      invert: r.invert === true || undefined,
      classId: finite(r.classId),
      depthLo: finite(r.depthLo),
      depthHi: finite(r.depthHi),
      threshold: finite(r.threshold),
      feather: finite(r.feather),
      adjust,
    });
  }
  return out.length > 0 ? out : undefined;
}

// The preset share-file envelope. Version 1; unknown newer versions are
// rejected with a readable error rather than half-imported.
interface PresetFile {
  marrawUserPresets: number;
  presets: UserPreset[];
}

export function userPresetsFileBlob(presets: UserPreset[]): Blob {
  const file: PresetFile = { marrawUserPresets: 1, presets };
  return new Blob([JSON.stringify(file, null, 2)], { type: 'application/json' });
}

// parseUserPresetsFile parses an imported share file: envelope checked,
// every preset sanitized as untrusted, fresh ids assigned (imports append —
// they never overwrite by id collision).
export function parseUserPresetsFile(text: string): UserPreset[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    throw new Error('not a JSON file');
  }
  if (typeof parsed !== 'object' || parsed === null || !('marrawUserPresets' in parsed)) {
    throw new Error('not a marraw preset file');
  }
  const file = parsed as PresetFile;
  if (typeof file.marrawUserPresets !== 'number' || file.marrawUserPresets > 1) {
    throw new Error('preset file from a newer marraw — update to import it');
  }
  return sanitizeUserPresets(file.presets).map((p) => ({ ...p, id: crypto.randomUUID() }));
}
