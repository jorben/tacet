// Schemes (presets), rule sets and their version history.
import type { ModelPlugin, PluginInfo, Preset, PresetContent, PresetTreatment, Ruleset } from "./types";
import { CATEGORIES, PRESET_CATEGORIES } from "./catalog";
import { engineCategory, uiCategory } from "./live";

/** Preserve engine ids for categories without a review-specific UI id. */
export const presetCategoryId = (engine: string): string => {
  const ui = uiCategory(engine);
  return ui === "other" ? engine : ui;
};
export const presetEngineCategory = (id: string): string => id in CATEGORIES ? engineCategory(id as keyof typeof CATEGORIES) : id;
const EXTRA_LABELS: Record<string, string> = { account: "账号", ip_address: "IP 地址", url: "网址", secret: "凭证", employee: "工号" };
export const presetCategoryLabel = (id: string): string => CATEGORIES[id as keyof typeof CATEGORIES]?.l ?? EXTRA_LABELS[id] ?? id;
export const presetTreatment = (p: Preset, id: string): PresetTreatment => p.cats[id] ?? "mask";

/** What detects a category in a scheme: the selected model, its active rules, or both. */
export interface CategorySource { model: boolean; rules: boolean }

export function presetCategorySources(p: Pick<Preset, "det" | "model" | "rules">, models: Pick<ModelPlugin, "id" | "categories">[], rulesets: Ruleset[]): Map<string, CategorySource> {
  const out = new Map<string, CategorySource>();
  const mark = (id: string, k: keyof CategorySource) => {
    if (id === "org") return;
    const s = out.get(id) ?? { model: false, rules: false };
    s[k] = true;
    out.set(id, s);
  };
  if (p.det === "model") models.find((m) => m.id === p.model)?.categories?.forEach((c) => mark(presetCategoryId(c), "model"));
  rulesets.filter((r) => p.rules.includes(r.id)).forEach((s) => s.rules.forEach((r) => {
    if (r.on) mark(presetCategoryId(r.ecat ?? engineCategory(r.cat)), "rules");
  }));
  return out;
}

export const categorySourceLabel = (s: CategorySource | undefined): string => (s?.model ? (s.rules ? "模型 + 规则" : "模型") : s?.rules ? "规则" : "");

/** Only categories detected by the selected model or active rules are offered. */
export function presetCategories(p: Pick<Preset, "det" | "model" | "rules">, models: Pick<ModelPlugin, "id" | "categories">[], rulesets: Ruleset[]): string[] {
  const covered = presetCategorySources(p, models, rulesets);
  const order: string[] = [...PRESET_CATEGORIES, "account", "ip_address", "url", "secret"];
  return [...covered.keys()].sort((a, b) => {
    const rank = (id: string) => { const i = order.indexOf(id); return i < 0 ? order.length : i; };
    return rank(a) - rank(b) || a.localeCompare(b);
  });
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
