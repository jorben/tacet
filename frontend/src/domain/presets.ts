// Schemes (presets), rule sets and their version history.
import type { ModelPlugin, PluginInfo, Preset, PresetContent, PresetTreatment, Rule, Ruleset } from "./types";
import { byCatalogOrder, STANDARD_CATEGORIES, type CategoryId, type CategoryInfo } from "./categories";

export const presetTreatment = (p: Preset, id: string): PresetTreatment => p.cats[id] ?? "mask";

/** What detects a category in a scheme: the selected model, its active rules, or both. */
export interface CategorySource { model: boolean; rules: boolean }

export function presetCategorySources(p: Pick<Preset, "det" | "model" | "rules">, models: Pick<ModelPlugin, "id" | "categories">[], rulesets: Ruleset[]): Map<string, CategorySource> {
  const out = new Map<string, CategorySource>();
  const mark = (id: string, k: keyof CategorySource) => {
    const s = out.get(id) ?? { model: false, rules: false };
    s[k] = true;
    out.set(id, s);
  };
  if (p.det === "model") models.find((m) => m.id === p.model)?.categories?.forEach((c) => mark(c, "model"));
  rulesets.filter((r) => p.rules.includes(r.id)).forEach((s) => s.rules.forEach((r) => {
    if (r.on) mark(r.cat, "rules");
  }));
  return out;
}

export const categorySourceLabel = (s: CategorySource | undefined): string => (s?.model ? (s.rules ? "模型 + 规则" : "模型") : s?.rules ? "规则" : "");

/**
 * Only categories detected by the selected model or active rules are offered, in catalog order. No list of
 * categories is fixed: whatever the model's label map or a rule reports (a user category included) is offered.
 */
export function presetCategories(p: Pick<Preset, "det" | "model" | "rules">, models: Pick<ModelPlugin, "id" | "categories">[], rulesets: Ruleset[], catalog: readonly CategoryInfo[] = STANDARD_CATEGORIES): string[] {
  return byCatalogOrder(catalog, presetCategorySources(p, models, rulesets).keys());
}

/** Model scores are not comparable across models: the default (calibrated) value and the floor come from the bound model. */
export const confidenceOf = (m: ModelPlugin | undefined): { rec: number; min: number } => ({ rec: m?.confRec ?? 0.5, min: m?.confMin ?? 0.3 });
export const formatConfidence = (v: number | string): string => (+v).toFixed(2);

export const modelById = (models: ModelPlugin[], id: string | null | undefined): ModelPlugin | undefined => models.find((m) => m.id === id);
export const findPresetByName = (presets: Preset[], name: string): Preset => presets.find((p) => p.name === name) || presets[0]!;
export const presetNeedsModel = (presets: Preset[], name: string): boolean => findPresetByName(presets, name).det === "model";
export const presetModelId = (presets: Preset[], name: string): string | null => (presetNeedsModel(presets, name) ? findPresetByName(presets, name).model : null);
export const presetConfidence = (p: Preset, models: ModelPlugin[]): number => p.conf ?? confidenceOf(modelById(models, p.model)).rec;

/** Rule sets that ship with a plugin are only available while the plugin is on. */
export const availableRulesets = (rulesets: Ruleset[], plugins: PluginInfo[]): Ruleset[] =>
  rulesets.filter((rs) => !rs.plugin || plugins.find((p) => p.id === rs.plugin)?.on);

/** The rules of a rule set that report one category. */
export interface RuleGroup {
  category: CategoryId;
  /** The catalog lists the category. */
  known: boolean;
  /**
   * The group's rules in rule-set order, each with its index in the rule set: editing, deleting and switching a
   * rule address it by that index (a rule not stored yet has no id).
   */
  items: { rule: Rule; index: number }[];
}

/**
 * The rules grouped by the category they report, in catalog order (the order the scheme page lists categories
 * in); categories the catalog does not know come last. A category no rule reports has no group.
 */
export function ruleGroups(rules: readonly Rule[], catalog: readonly CategoryInfo[]): RuleGroup[] {
  const byCategory = new Map<CategoryId, RuleGroup["items"]>();
  rules.forEach((rule, index) => {
    const items = byCategory.get(rule.cat) ?? [];
    items.push({ rule, index });
    byCategory.set(rule.cat, items);
  });
  return byCatalogOrder(catalog, byCategory.keys()).map((category) => ({
    category, known: catalog.some((c) => c.id === category), items: byCategory.get(category)!,
  }));
}

/** How many rules of a group are on, of how many. */
export const ruleGroupCount = (g: Pick<RuleGroup, "items">): { on: number; total: number } =>
  ({ on: g.items.filter((x) => x.rule.on).length, total: g.items.length });

/**
 * The scheme masks or replaces the category. A setting the scheme holds counts; without one, a scheme not stored
 * yet masks it (the screens' default), while a stored scheme does not handle it: the kernel only acts on the
 * categories its stored scheme lists, and a category nothing detected was not written there.
 */
export function presetHandles(p: Pick<Preset, "cats" | "stored">, category: CategoryId): boolean {
  const t = p.cats[category] ?? (p.stored ? undefined : "mask");
  return t === "mask" || t === "ph";
}

/** What the schemes detect with: the rule sets they may choose from and the models with their categories. */
export interface DetectionSources {
  rulesets: Ruleset[];
  models: Pick<ModelPlugin, "id" | "categories">[];
}

/** What a change of rules or of a model's labels would cost the schemes it affects. */
export interface DetectionLoss {
  /**
   * Schemes that mask or replace a category nothing would detect in them any more: the kernel refuses to start
   * a task with them (no detection source).
   */
  categories: { preset: Preset; categories: CategoryId[] }[];
  /** Schemes whose rule sets would have no rule switched on between them: a task with them fails. */
  noRules: Preset[];
}

export const losesNothing = (l: DetectionLoss): boolean => !l.categories.length && !l.noRules.length;

const rulesOn = (p: Pick<Preset, "rules">, rulesets: Ruleset[]): number =>
  rulesets.filter((r) => p.rules.includes(r.id)).reduce((n, r) => n + r.rules.filter((x) => x.on).length, 0);

/**
 * Compares, for each scheme `affects` picks, what detects its categories before and after a change: each scheme
 * counts its own detection (rules only leaves the model out) and all its rule sets.
 */
export function detectionLoss(presets: Preset[], before: DetectionSources, after: DetectionSources, affects: (p: Preset) => boolean): DetectionLoss {
  const out: DetectionLoss = { categories: [], noRules: [] };
  presets.filter(affects).forEach((p) => {
    const was = presetCategorySources(p, before.models, before.rulesets);
    const now = presetCategorySources(p, after.models, after.rulesets);
    const lost = [...was.keys()].filter((c) => !now.has(c) && presetHandles(p, c));
    if (lost.length) out.categories.push({ preset: p, categories: lost });
    if (rulesOn(p, before.rulesets) > 0 && rulesOn(p, after.rulesets) === 0) out.noRules.push(p);
  });
  return out;
}

/** What changing a rule set to `changed` would cost the schemes that choose it (as their first rule set or another). */
export function rulesetChangeLoss(presets: Preset[], sources: DetectionSources, changed: Ruleset): DetectionLoss {
  const after = sources.rulesets.map((r) => (r.id === changed.id ? changed : r));
  return detectionLoss(presets, sources, { ...sources, rulesets: after }, (p) => p.rules.includes(changed.id));
}

/** What giving a model the label map `map` would cost the schemes that detect with it. */
export function mappingChangeLoss(presets: Preset[], sources: DetectionSources, modelId: string, map: Record<string, string>): DetectionLoss {
  const categories = [...new Set(Object.values(map))];
  const after = sources.models.map((m) => (m.id === modelId ? { id: m.id, categories } : m));
  return detectionLoss(presets, sources, { ...sources, models: after }, (p) => p.det === "model" && p.model === modelId);
}

/** The schemes `uses` picks that mask or replace `category` while nothing detects it in them now. */
export function presetsWithoutSource(presets: Preset[], sources: DetectionSources, uses: (p: Preset) => boolean, category: CategoryId): Preset[] {
  return presets.filter((p) => uses(p) && presetHandles(p, category) && !presetCategorySources(p, sources.models, sources.rulesets).has(category));
}

export function ruleSummary(r: { kind: string; n?: number; body: string }): string {
  return r.kind === "词表" ? (r.n || r.body.split("\n").filter((x) => x.trim()).length) + " 条" : r.body;
}

export type Versioned = Preset | Ruleset;

/** The versioned content of an object: a scheme holds detection settings, a rule set holds its rules. */
export function versionedContent(o: Preset): PresetContent;
export function versionedContent(o: Ruleset): { rules: Ruleset["rules"] };
export function versionedContent(o: Versioned): PresetContent | { rules: Ruleset["rules"] } {
  if ("cats" in o) {
    const { det, model, conf, rules, cats, img } = o;
    return JSON.parse(JSON.stringify({ det, model, conf, rules, cats, img }));
  }
  return JSON.parse(JSON.stringify({ rules: o.rules }));
}

/** Save the previous content before an edit; keep four earlier versions plus the current one. */
export function bumpVersion(o: Versioned): void {
  const meta = { ver: o.ver, when: o.edited || "更早" };
  if ("cats" in o) o.hist = [{ ...meta, data: versionedContent(o) }, ...(o.hist || [])].slice(0, 4);
  else o.hist = [{ ...meta, data: versionedContent(o) }, ...(o.hist || [])].slice(0, 4);
  o.ver = "v" + (parseInt(o.ver.slice(1), 10) + 1);
  o.edited = "刚刚";
}

/** A fresh copy under a new name (“复制”). */
export function duplicate<T extends Versioned>(o: T, id: string): T {
  const copy = { ...JSON.parse(JSON.stringify(o)), id, name: o.name + " 副本", ver: "v1", builtin: false, plugin: null, edited: "刚刚", hist: [], stored: undefined };
  // a copy is a new object: it is stored under new ids, and its rules get ids of their own
  if (Array.isArray(copy.rules) && copy.rules.length && typeof copy.rules[0] === "object") copy.rules.forEach((r: { rid?: string }) => { delete r.rid; });
  return copy;
}
