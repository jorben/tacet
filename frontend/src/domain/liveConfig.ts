// How the screens' schemes and rule sets are stored by the kernel, and back. Pure: no store, no service.
//
// A screen "scheme" is two stored objects: a preset (which rule sets, which model, how confident) and a
// scheme (what is done to each category). The screen shows them as one. A rule set maps one to one.
import type { ConfigItem, EngineModelRuntime, EnginePlugin, EngineSettings } from "../services/types";
import { STANDARD_CATEGORIES, type CategoryInfo } from "./categories";
import { presetCategories, presetCategorySources, presetTreatment, schemeCategories } from "./presets";
import type {
  KeepDays, ModelMapping, PluginInfo, RedactStyle, Settings, ThemePref,
  Boundary, ImageAction, ModelErrorKind, ModelPlugin, ModelStatus, Preset, PresetTreatment, Rule, Ruleset,
} from "./types";

interface RuleJSON {
  id: string;
  name?: string;
  category: string;
  pattern?: string;
  terms?: string[];
  boundary?: string;
  validator?: string;
  suspect?: boolean;
  fold_case?: boolean;
  context?: string[];
  context_window?: number;
  disabled?: boolean;
}

const parse = <T,>(text: string, fallback: T): T => {
  try { return JSON.parse(text) as T; } catch { return fallback; }
};

/* ── rule sets ────────────────────────────────────────────────────────────── */

/** Settings of a rule the screen has no control for; they travel with the rule so that saving does not lose them. */
const EXT_KEYS = ["validator", "suspect", "fold_case", "context", "context_window"] as const;

export const BUILTIN_MODEL_PROFILE = "builtin-model-astrlink";
export const BUILTIN_MODEL_PRESET = "builtin-preset-model";
export const BUILTIN_RULES_PRESET = "builtin-preset-office";

export const schemeIdOf = (preset: ConfigItem): string => parse<{ scheme_id?: string }>(preset.content, {}).scheme_id ?? "";

/** Two stored contents mean the same when their parsed JSON is equal (the kernel stores a canonical form). */
export function sameContent(a: string, b: string): boolean {
  const norm = (v: unknown): unknown =>
    Array.isArray(v) ? v.map(norm) : v && typeof v === "object" ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, norm((v as Record<string, unknown>)[k])])) : v;
  try { return JSON.stringify(norm(JSON.parse(a))) === JSON.stringify(norm(JSON.parse(b))); } catch { return false; }
}

const ruleFromJSON = (r: RuleJSON): Rule => {
  const kind = r.terms?.length ? "词表" : r.validator ? "校验" : "正则";
  const body = r.terms?.length ? r.terms.join("\n") : r.validator ? `${r.pattern ?? ""}${r.validator ? "  ·  校验 " + r.validator : ""}` : (r.pattern ?? "");
  return {
    name: r.name || r.id, kind, cat: r.category, body, bd: (["digit", "word"].includes(r.boundary ?? "") ? r.boundary : "") as Boundary,
    on: !r.disabled, rid: r.id, ext: extOf(r),
  };
};

function extOf(r: RuleJSON): Record<string, unknown> | undefined {
  const out: Record<string, unknown> = {};
  for (const k of EXT_KEYS) if (r[k] !== undefined) out[k] = r[k];
  if (r.boundary === "cjk") out.boundary = "cjk";
  return Object.keys(out).length ? out : undefined;
}

export const versionLabel = (it: Pick<ConfigItem, "readOnly" | "version">): string => (it.readOnly ? "内建" : "v" + it.version);

export function rulesetFromItem(it: ConfigItem): Ruleset {
  const content = parse<{ rules?: RuleJSON[] }>(it.content, {});
  return {
    id: it.id, name: it.name, ver: versionLabel(it), builtin: it.readOnly, rules: (content.rules ?? []).map(ruleFromJSON), edited: "",
    hist: [], stored: { id: it.id, version: it.version },
  };
}

/** The stored form of a rule set. Rules keep their stored ids; new ones get an id no other rule of the set uses. */
export function rulesetContent(rs: Pick<Ruleset, "id" | "rules">): string {
  const used = new Set(rs.rules.map((r) => r.rid).filter(Boolean));
  const seed = rs.id.replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 80);
  let n = 0;
  const fresh = (): string => {
    let id: string;
    do id = `${seed}.${++n}`; while (used.has(id));
    used.add(id);
    return id;
  };
  const rules: RuleJSON[] = rs.rules.map((r) => {
    const id = r.rid ?? fresh();
    const out: RuleJSON = { id, category: r.cat };
    if (r.name && r.name !== id) out.name = r.name;
    if (r.kind === "词表") out.terms = r.body.split("\n").map((t) => t.trim()).filter(Boolean);
    else out.pattern = r.body.replace(/\s+·\s+校验.*$/s, "");
    if (r.bd) out.boundary = r.bd;
    if (!r.on) out.disabled = true;
    return { ...out, ...(r.ext as Partial<RuleJSON> | undefined) };
  });
  return JSON.stringify({ rules });
}

/** Categories a set of rule sets detects (rules that are switched off do not count). */
export function ruleCategories(sets: Pick<Ruleset, "rules">[]): Set<string> {
  const out = new Set<string>();
  sets.forEach((s) => s.rules.forEach((r) => { if (r.on) out.add(r.cat); }));
  return out;
}

/* ── schemes ──────────────────────────────────────────────────────────────── */

export interface Model {
  /** Screen id of the model (the engine's model id, e.g. astrlink-guard-int8). */
  id: string;
  /** Engine id of the stored model profile. */
  profile: string;
  /** What a run reserves for it, in bytes. */
  memory: number;
  floor: number;
  calibrated: number;
  /** Categories its labels produce now (the user's mapping, else the default). */
  categories: string[];
}

/** The label map a model runs with: the user's mapping when there is one, else the default. */
export const effectiveLabels = (m: Pick<ModelPlugin, "labels" | "mapping">): Record<string, string> => m.mapping?.map ?? m.labels ?? {};

/** The categories a label map produces, sorted. */
export const mappedCategories = (map: Record<string, string>): string[] => [...new Set(Object.values(map))].sort();

/** The labels a label map sends to `category`, sorted: a category may have several labels, a label has one category. */
export const labelsOfCategory = (map: Record<string, string>, category: string): string[] =>
  Object.keys(map).filter((l) => map[l] === category).sort();

/**
 * Who uses a user category, for deleting it. Rule sets whose rules report it keep it: it cannot be deleted
 * while they do. Models whose mapping sends labels to it let go of it: those labels are no longer used. A model
 * all of whose counted labels send to it would count none, so it keeps the category too (`last`).
 */
export interface CategoryUse {
  rulesets: string[];
  models: { name: string; labels: string[]; last: boolean }[];
}

export function categoryUse(id: string, rulesets: Pick<Ruleset, "name" | "rules">[], models: Pick<ModelPlugin, "name" | "labels" | "mapping">[]): CategoryUse {
  return {
    rulesets: rulesets.filter((r) => r.rules.some((x) => x.cat === id)).map((r) => r.name),
    models: models.flatMap((m) => {
      const map = effectiveLabels(m);
      const labels = labelsOfCategory(map, id);
      return labels.length ? [{ name: m.name, labels, last: labels.length === Object.keys(map).length }] : [];
    }),
  };
}

/** The labels of a model its current mapping sends to no category (what they find is dropped), sorted. */
export function unmappedLabels(m: Pick<ModelPlugin, "labels" | "mapping">): string[] {
  const map = effectiveLabels(m);
  return Object.keys(m.labels ?? {}).filter((l) => !(l in map)).sort();
}

/**
 * `map` with exactly `labels` sending to `category`. A label has one category, so a chosen label of another
 * category moves here (`moved` lists it with where it came from); a label of this category left out is no longer
 * used. Labels the model does not emit (`known`) are ignored.
 */
export function withCategoryLabels(map: Record<string, string>, category: string, labels: readonly string[], known: Record<string, string>): { map: Record<string, string>; moved: { label: string; from: string }[] } {
  const want = new Set(labels.filter((l) => l in known));
  const out = { ...map };
  for (const [l, c] of Object.entries(map)) if (c === category && !want.has(l)) delete out[l];
  const moved: { label: string; from: string }[] = [];
  for (const l of [...want].sort()) {
    const from = map[l];
    if (from != null && from !== category) moved.push({ label: l, from });
    out[l] = category;
  }
  return { map: out, moved };
}

/** Two label maps are the same mapping. */
export const sameMapping = (a: Record<string, string>, b: Record<string, string>): boolean =>
  Object.keys(a).length === Object.keys(b).length && Object.entries(a).every(([k, v]) => b[k] === v);

/** The user's mapping of a model from its stored object, if it is one of this model's. */
export function mappingFromItem(it: ConfigItem | undefined, modelId: string): ModelMapping | undefined {
  if (!it) return undefined;
  const c = parse<{ model_id?: string; label_map?: Record<string, string> }>(it.content, {});
  if (c.model_id !== modelId || !c.label_map) return undefined;
  return { map: { ...c.label_map }, stored: { id: it.id, version: it.version } };
}

const MODEL_STATUS: Record<string, ModelStatus> = {
  pending: "missing", downloading: "downloading", paused: "paused", verifying: "verifying", preparing: "loading",
  // installed: its files were verified and tried when downloaded; every run verifies them again before loading
  installed: "ready", ready: "ready", error: "error",
};
const MODEL_ERROR: Record<string, ModelErrorKind> = {
  MODEL_NETWORK: "offline", MODEL_DISK_SPACE: "disk", MODEL_VERIFY_FAILED: "integrity", MODEL_FILES_CHANGED: "integrity",
  MODEL_STORE_FAILED: "integrity", MODEL_PREPARE_FAILED: "load",
};

/** The download state, progress and runtime of a model as the screens keep them. */
/** Screen status, progress and error kind of an engine download state. */
const downloadState = (b: { state: string; code: string; bytes: number; downloaded: number }) => {
  const status = MODEL_STATUS[b.state] ?? "missing";
  return {
    status,
    progress: b.bytes > 0 ? Math.min(100, Math.floor((b.downloaded * 100) / b.bytes)) : 0,
    error: status === "error" ? MODEL_ERROR[b.code] ?? "offline" : null,
  };
};

export function modelStateFromEngine(rt: EngineModelRuntime): Pick<ModelPlugin, "status" | "progress" | "error" | "eligible" | "minMemoryGiB" | "runtime"> {
  return {
    ...downloadState(rt),
    eligible: rt.eligible !== false,
    minMemoryGiB: rt.minMemory ? Math.round(rt.minMemory / 2 ** 30) : 0,
    runtime: {
      dir: rt.dir, backend: rt.backend, device: rt.device === "auto" ? "auto" : rt.device === "gpu" ? "gpu" : "cpu", repository: rt.repository ?? "",
      detecting: rt.device === "auto" && !!rt.detecting,
      ...(rt.device === "auto" && rt.deviceCode ? { deviceCode: rt.deviceCode } : {}),
      accelerable: !!rt.accelerable, accelerated: !!rt.accelerated,
      ...(rt.accel ? { accel: { id: rt.accel.id, variant: rt.accel.variant, weightMiB: Math.round(rt.accel.bytes / 2 ** 20), ...downloadState(rt.accel) } } : {}),
    },
  };
}

/** A stored model profile and, when the engine lists it, the model's catalog entry. */
export function modelFromItem(it: ConfigItem, rt?: EngineModelRuntime, mappings: ConfigItem[] = []): { plugin: ModelPlugin; model: Model } | null {
  const c = parse<{ model_id?: string; adapter_floor?: number; calibrated?: number; label_map?: Record<string, string>; memory_bytes?: number }>(it.content, {});
  if (!c.model_id || c.adapter_floor == null || c.calibrated == null) return null;
  const labels = { ...(c.label_map ?? {}) };
  const mapping = mappingFromItem(mappings.find((m) => m.name === c.model_id), c.model_id);
  const model: Model = {
    id: c.model_id, profile: it.id, memory: c.memory_bytes ?? 0, floor: c.adapter_floor, calibrated: c.calibrated,
    categories: mappedCategories(effectiveLabels({ labels, mapping })),
  };
  const base: ModelPlugin = {
    id: c.model_id, name: rt?.name || it.name, version: rt?.version ?? "", variant: rt?.variant ?? "", languages: rt?.languages ?? "",
    weightMiB: rt ? Math.round(rt.bytes / 2 ** 20) : 0, recommended: it.id === BUILTIN_MODEL_PROFILE,
    categories: model.categories, labels, ...(mapping ? { mapping } : {}),
    confRec: c.calibrated, confMin: c.adapter_floor, status: "ready", progress: 100, error: null,
  };
  return { model, plugin: rt ? { ...base, ...modelStateFromEngine(rt) } : base };
}

interface SchemeJSON { category_actions: Record<string, string>; image_default: string; human_review?: string[] }
interface PresetJSON {
  ruleset_id: string;
  extra_ruleset_ids?: string[];
  scheme_id: string;
  scheme_rev?: number;
  model_profile_id?: string;
  mode: string;
  rules_only: boolean;
  min_confidence?: number;
  memory_bytes: number;
  dpi: number;
  style: string;
  retention_days: number;
}

const toTreatment = (a: string | undefined): PresetTreatment | null => (a === "placeholder" ? "ph" : a === "mask" || a === "delete" ? "mask" : null);

/** The settings a stored scheme object holds, one for each category it lists (an action the screen has no setting for reads as off). */
export function schemeTreatments(scheme: ConfigItem | undefined): Preset["cats"] {
  const actions = parse<Partial<SchemeJSON>>(scheme?.content ?? "{}", {}).category_actions ?? {};
  return Object.fromEntries(Object.entries(actions).map(([id, action]) => [id, toTreatment(action) ?? "off"]));
}

/** The screen's scheme from its two stored objects. */
export function presetFromItems(preset: ConfigItem, scheme: ConfigItem | undefined, rulesets: Ruleset[], models: Model[]): Preset {
  const p = parse<PresetJSON>(preset.content, {} as PresetJSON);
  const model = models.find((m) => m.profile === p.model_profile_id) ?? null;
  const s = { category_actions: {}, image_default: "remove", ...parse<Partial<SchemeJSON>>(scheme?.content ?? "{}", {}) } as SchemeJSON;
  const det = p.rules_only || !model ? "rules" : "model";
  const cats = schemeTreatments(scheme);
  const ids = [p.ruleset_id, ...(p.extra_ruleset_ids ?? [])].filter((id) => rulesets.some((r) => r.id === id));
  for (const id of presetCategories({ det, model: model?.id ?? "", rules: ids }, models, rulesets)) {
    // An absent action is disabled, including a category explicitly switched off before saving.
    cats[id] ??= "off";
  }
  return {
    id: preset.id, name: preset.name, ver: versionLabel(preset), builtin: preset.readOnly, det, model: model?.id ?? models[0]?.id ?? "",
    conf: det === "model" ? (p.min_confidence ?? model?.calibrated ?? null) : null,
    rules: ids, cats, img: (s.image_default === "review" ? "keep" : "remove") as ImageAction, edited: "", hist: [],
    stored: { id: preset.id, version: preset.version, schemeId: scheme?.id, schemeVersion: scheme?.version },
  };
}

export interface Written {
  scheme: string;
  /** `schemeRev` is the stored version of the scheme object this version of the preset goes with. */
  preset: (schemeId: string, schemeRev?: number) => string;
}

/**
 * The stored objects of a screen scheme. `previous` holds the stored scheme and preset so that settings the
 * screen has no control for (resource profile, categories the scheme does not list) are kept as they were.
 * A listed category nothing detects keeps its setting: the kernel leaves it out of a run until a source
 * comes back. A scheme not stored yet writes only an explicit setting for it, never the default mask.
 */
export function presetContents(p: Preset, rulesets: Ruleset[], models: Model[], previous?: { scheme?: ConfigItem; preset?: ConfigItem }, catalog: readonly CategoryInfo[] = STANDARD_CATEGORIES): Written {
  const model = models.find((m) => m.id === p.model) ?? null;
  const prevScheme = { category_actions: {}, image_default: "remove", ...parse<Partial<SchemeJSON>>(previous?.scheme?.content ?? "{}", {}) } as SchemeJSON;
  const prevPreset = parse<Partial<PresetJSON>>(previous?.preset?.content ?? "{}", {});
  const chosen = p.rules.map((id) => rulesets.find((r) => r.id === id)).filter((r): r is Ruleset => !!r);
  const useModel = p.det === "model" && !!model;

  const listed = schemeCategories(p, models, chosen, catalog);
  // Categories without a row keep their stored actions verbatim, including keep (read as off).
  const actions: Record<string, string> = Object.fromEntries(
    Object.entries(prevScheme.category_actions).filter(([c]) => !listed.includes(c)),
  );
  const sources = presetCategorySources(p, models, chosen);
  for (const c of listed) {
    const sourced = sources.has(c);
    // a category nothing detects cannot be changed on screen: what is stored for it stays as it is, and an
    // unsaved scheme writes only an explicit setting (never the default mask, which the screen shows as off)
    if (!sourced && c in prevScheme.category_actions) { actions[c] = prevScheme.category_actions[c]!; continue; }
    const t = sourced ? presetTreatment(p, c) : p.cats[c];
    if (t !== "mask" && t !== "ph") continue;
    actions[c] = t === "ph" ? "placeholder" : prevScheme.category_actions[c] === "delete" ? "delete" : "mask";
  }
  const scheme: SchemeJSON = { category_actions: actions, image_default: p.img === "keep" ? "review" : "remove" };
  if (prevScheme.human_review?.length) scheme.human_review = prevScheme.human_review.filter((c) => c in actions);

  const [primary, ...extra] = chosen.map((r) => r.stored?.id ?? r.id);
  const preset = (schemeId: string, schemeRev?: number): string => {
    const out: PresetJSON = {
      ruleset_id: primary ?? "", scheme_id: schemeId, mode: prevPreset.mode ?? "guided", rules_only: !useModel,
      // the budget must hold the model the scheme now uses (a larger model raises it)
      memory_bytes: Math.max(prevPreset.memory_bytes ?? 1 << 30, useModel ? model!.memory : 0), dpi: prevPreset.dpi ?? 200, style: prevPreset.style ?? "default", retention_days: prevPreset.retention_days ?? 0,
    };
    if (extra.length) out.extra_ruleset_ids = extra;
    if (schemeRev) out.scheme_rev = schemeRev;
    if (useModel) {
      out.model_profile_id = model!.profile;
      out.min_confidence = Math.max(p.conf ?? model!.calibrated, model!.floor);
    }
    return JSON.stringify(out);
  };
  return { scheme: JSON.stringify(scheme), preset };
}

/* ── exchange files ───────────────────────────────────────────────────────── */

const PACKAGE_VERSION = 1;

/** A user category as an exchange file carries it: ids are of the workspace that exported it. */
export interface CategoryDef { id: string; name: string; placeholder: string; color?: string }

/** The definitions of the user categories among `ids`, so that another workspace can adopt them. */
function userCategoryDefs(ids: Iterable<string>, catalog: readonly CategoryInfo[]): CategoryDef[] {
  const want = new Set(ids);
  return catalog.filter((c) => c.origin === "custom" && want.has(c.id))
    .map((c) => ({ id: c.id, name: c.name, placeholder: c.placeholder, ...(c.color ? { color: c.color } : {}) }));
}

/** A rule set as an exchange file: current content only, no ids, no history (FR-19); the user categories it reports go along. */
export function exportRuleset(rs: Pick<Ruleset, "id" | "name" | "rules">, catalog: readonly CategoryInfo[] = []): string {
  const categories = userCategoryDefs(rs.rules.map((r) => r.cat), catalog);
  return JSON.stringify({ tacet_config: PACKAGE_VERSION, kind: "ruleset", name: rs.name, ...(categories.length ? { categories } : {}), ...(JSON.parse(rulesetContent(rs)) as object) }, null, 2);
}

/** The user category definitions an exchange file carries; malformed entries are left out. */
export function packageCategories(text: string): CategoryDef[] {
  let p: { categories?: unknown };
  try { p = JSON.parse(text); } catch { return []; }
  if (!Array.isArray(p?.categories)) return [];
  return (p.categories as Partial<CategoryDef>[]).filter((c): c is CategoryDef =>
    !!c && typeof c.id === "string" && !!c.id && typeof c.name === "string" && !!c.name.trim() && typeof c.placeholder === "string" && !!c.placeholder);
}

/**
 * The exchange file with its category ids replaced as `map` says (the exporting workspace's ids → this
 * workspace's), in rules and in scheme actions. Ids not in the map stay.
 */
export function remapPackageCategories(text: string, map: ReadonlyMap<string, string>): string {
  const p = JSON.parse(text) as { rules?: RuleJSON[]; scheme?: { category_actions?: Record<string, string>; human_review?: string[] }; categories?: unknown };
  const to = (id: string) => map.get(id) ?? id;
  p.rules?.forEach((r) => { if (typeof r?.category === "string") r.category = to(r.category); });
  if (p.scheme?.category_actions) p.scheme.category_actions = Object.fromEntries(Object.entries(p.scheme.category_actions).map(([k, v]) => [to(k), v]));
  if (Array.isArray(p.scheme?.human_review)) p.scheme!.human_review = p.scheme!.human_review.map(to);
  delete p.categories;
  return JSON.stringify(p);
}

/** A scheme as an exchange file: rule sets are named, not referenced by id; the model is not included. */
export function exportScheme(p: Preset, rulesets: Ruleset[], models: Model[] = [{ id: p.model, profile: "", memory: 0, floor: 0, calibrated: p.conf ?? 0.5, categories: Object.keys(p.cats) }], catalog: readonly CategoryInfo[] = []): string {
  const w = presetContents(p, rulesets, models);
  const preset = JSON.parse(w.preset("")) as PresetJSON;
  const names = p.rules.map((id) => rulesets.find((r) => r.id === id)?.name).filter((n): n is string => !!n);
  const categories = userCategoryDefs(Object.keys((JSON.parse(w.scheme) as SchemeJSON).category_actions), catalog);
  return JSON.stringify({
    tacet_config: PACKAGE_VERSION, kind: "scheme", name: p.name, ...(categories.length ? { categories } : {}), rulesets: names, confidence: p.conf, detection: p.det,
    scheme: JSON.parse(w.scheme), settings: { mode: preset.mode, memory_bytes: preset.memory_bytes, dpi: preset.dpi, style: preset.style, retention_days: preset.retention_days },
  }, null, 2);
}

export type Imported =
  | { kind: "ruleset"; name: string; ruleset: Ruleset }
  | { kind: "scheme"; name: string; preset: Preset }
  | { error: "format" | "kind" | "rulesets" | "content"; detail?: string };

const item = (kind: string, name: string, content: unknown): ConfigItem => ({ id: "", kind, name, note: "", version: 1, readOnly: false, content: JSON.stringify(content) });

/** Reads an exchange file. Nothing in it names an object of this installation except rule sets, by name. */
export function importPackage(text: string, want: "ruleset" | "preset", rulesets: Ruleset[], models: Model[]): Imported {
  const model = models[0] ?? null; // an exchange file names no model: a model scheme takes the default one
  let p: Record<string, unknown>;
  try { p = JSON.parse(text); } catch { return { error: "format" }; }
  if (!p || typeof p !== "object" || p.tacet_config !== PACKAGE_VERSION || typeof p.name !== "string" || !p.name.trim() || p.name.length > 256) return { error: "format" };
  if (want === "ruleset") {
    if (p.kind !== "ruleset" || !Array.isArray(p.rules)) return { error: "kind" };
    const rs = rulesetFromItem(item("ruleset", p.name, { rules: p.rules }));
    if (rs.rules.length > 5000 || rs.rules.some((r) => !r.name || typeof r.body !== "string" || !r.body.trim())) return { error: "content" };
    return { kind: "ruleset", name: p.name, ruleset: { ...rs, ver: "v1", stored: undefined } };
  }
  if (p.kind !== "scheme" || !p.scheme || !Array.isArray(p.rulesets)) return { error: "kind" };
  const ids: string[] = [];
  for (const n of p.rulesets as unknown[]) {
    const rs = rulesets.find((r) => r.name === n);
    if (!rs) return { error: "rulesets", detail: String(n) };
    ids.push(rs.id);
  }
  if (!ids.length) return { error: "rulesets" };
  const s = (p.settings ?? {}) as Partial<PresetJSON>;
  const useModel = p.detection === "model" && !!model;
  const presetJSON: PresetJSON = {
    ruleset_id: ids[0]!, scheme_id: "", mode: "guided", rules_only: !useModel, memory_bytes: Math.max(s.memory_bytes ?? 1 << 30, useModel ? model!.memory : 0), dpi: s.dpi ?? 200, style: s.style ?? "default",
    retention_days: s.retention_days ?? 0, ...(ids.length > 1 ? { extra_ruleset_ids: ids.slice(1) } : {}),
    ...(useModel ? { model_profile_id: model!.profile, min_confidence: typeof p.confidence === "number" ? p.confidence : model!.calibrated } : {}),
  };
  const preset = presetFromItems(item("preset", p.name, presetJSON), item("scheme", p.name, p.scheme), rulesets, models);
  return { kind: "scheme", name: p.name, preset: { ...preset, ver: "v1", stored: undefined, builtin: false } };
}

/* ── application preferences ──────────────────────────────────────────────── */

export function settingsFromEngine(e: EngineSettings): { settings: Settings; theme: ThemePref } {
  const keep = String(e.retentionDays) as KeepDays;
  return {
    settings: {
      redact: e.redactStyle as RedactStyle, tier: e.resourceTier === "std" ? "std" : "comfort", autoUpd: e.autoUpdate,
      keep: keep === "0" || keep === "30" ? keep : "7", closeAct: e.closeBehavior === "quit" ? "quit" : "tray", gpu: e.gpuAcceleration !== false,
    },
    theme: e.theme === "light" || e.theme === "dark" ? e.theme : "system",
  };
}

export const settingsToEngine = (s: Settings, theme: ThemePref): EngineSettings => ({
  closeBehavior: s.closeAct, theme, redactStyle: s.redact || "black", resourceTier: s.tier, retentionDays: Number(s.keep), autoUpdate: s.autoUpd, gpuAcceleration: s.gpu,
});

/* ── plugins ──────────────────────────────────────────────────────────────── */

const PLUGIN_NAMES: Record<string, string> = {
  "tacet.docx": "DOCX", "tacet.pdf": "PDF · 文字型", "tacet.text": "文本 · Markdown · 日志", "tacet.csv": "CSV", "tacet.json": "JSON · JSONL",
  "tacet.xlsx": "XLSX · 值子集", "tacet.astrlink": "AstrLink Guard 检测",
};

export function pluginFromEngine(p: EnginePlugin): PluginInfo {
  const roles = p.roles ?? [];
  const role = roles.includes("detect") ? "模型" : roles.includes("format") ? "格式" : "规则";
  return {
    id: p.id, name: PLUGIN_NAMES[p.id] ?? p.id, role, fmt: (p.formats ?? []).map((f) => "." + f).join(" "), ver: p.version, builtin: p.builtin, on: p.enabled,
  };
}
