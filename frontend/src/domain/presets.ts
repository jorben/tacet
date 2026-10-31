// Schemes (presets), rule sets and their version history.
import type { ModelPlugin, PluginInfo, Preset, PresetContent, Ruleset } from "./types";

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
