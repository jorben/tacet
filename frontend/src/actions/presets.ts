// Schemes (presets) and rule sets: editing, versions, import and the menus of the configuration pages.
import { MODEL_LABELS, OFFICE_CATEGORIES } from "../domain/catalog";
import {
  availableRulesets, bumpVersion, confidenceOf, duplicate, formatConfidence, modelById, presetConfidence, type Versioned,
} from "../domain/presets";
import type { Boundary, CategoryId, OfficeCategoryId, Preset, PresetTreatment, Rule, RuleKind, Ruleset } from "../domain/types";
import { getState, store, type AppState } from "../store";
import { closeModal, openMenu, openModal, toast, type PopAnchor } from "../overlay/store";
import { openSubpage } from "./navigation";
import { nextId } from "./timers";
import { isLive } from "./live";
import { exportLive, finishImportLive, importLive, openVersions, rollbackLive } from "./liveConfig";
import type { Draft } from "immer";

const allCategories = <T,>(v: T): Record<OfficeCategoryId, T> => Object.fromEntries(OFFICE_CATEGORIES.map((t) => [t, v])) as Record<OfficeCategoryId, T>;

export const selectedPreset = (s: AppState): Preset => s.presets.find((x) => x.id === s.presetSel) || s.presets[0]!;
export function selectedRuleset(s: AppState): Ruleset {
  const sets = availableRulesets(s.rulesets, s.plugins);
  return sets.find((x) => x.id === s.rulesetSel) || sets[0]!;
}

const presetOf = (d: Draft<AppState>): Draft<Preset> => d.presets.find((x) => x.id === d.presetSel) || d.presets[0]!;
const rulesetOf = (d: Draft<AppState>): Draft<Ruleset> => {
  const sets = availableRulesets(d.rulesets as Ruleset[], d.plugins as AppState["plugins"]);
  const id = (sets.find((x) => x.id === d.rulesetSel) || sets[0]!).id;
  return d.rulesets.find((x) => x.id === id)!;
};

export function setPresetTab(tab: "scheme" | "rules"): void {
  store.update((d) => { d.presetTab = tab; });
}

/* ── Schemes ──────────────────────────────────────────────────────────────── */

export function selectPreset(id: string): void {
  store.update((d) => { d.presetSel = id; });
}

export function newPreset(): void {
  const id = nextId("p");
  store.update((d) => {
    d.presets.push({ id, name: "新方案", ver: "v1", det: "model", model: "astr", conf: 0.5, rules: ["cn"], cats: allCategories<PresetTreatment>("mask"), img: "remove", edited: "刚刚" });
    d.presetSel = id;
  });
}

export function copyPreset(): void {
  store.update((d) => {
    const copy = duplicate(presetOf(d) as Preset, nextId("p"));
    d.presets.push(copy);
    d.presetSel = copy.id;
  });
  toast("已复制");
}

export function setDetection(det: "model" | "rules"): void {
  store.update((d) => {
    const p = presetOf(d);
    if (p.det === det) return;
    bumpVersion(p);
    p.det = det;
    (["org", "name", "addr"] as const).forEach((t) => { p.cats[t] = det === "rules" ? "na" : p.cats[t] === "na" ? "mask" : p.cats[t]; });
    if (det === "model" && p.conf == null) p.conf = confidenceOf(modelById(d.models, p.model)).rec;
  });
}

export function setCategoryTreatment(t: OfficeCategoryId, k: PresetTreatment): void {
  store.update((d) => {
    const p = presetOf(d);
    if (p.cats[t] === k) return;
    bumpVersion(p);
    p.cats[t] = k;
  });
}

export function openPresetModelMenu(anchor: PopAnchor): void {
  const s = getState();
  const p = selectedPreset(s);
  openMenu(anchor, [
    { h: "检测模型" },
    ...s.models.map((m) => ({ l: m.name, s: m.languages + " · " + MODEL_LABELS[m.status], k: m.id, on: p.model === m.id })),
    "-",
    { l: "管理模型插件", ic: "puzzle", k: "_p" },
  ], (it) => {
    if (it.k === "_p") { openSubpage("plugins"); return; }
    if (p.builtin || p.model === it.k) return;
    let note: string | null = null;
    store.update((d) => {
      const x = presetOf(d);
      bumpVersion(x);
      x.model = String(it.k);
      // A threshold means nothing on another model's scale, so switching models resets it to the new default.
      const was = presetConfidence(x as Preset, d.models);
      const model = modelById(d.models, x.model);
      const rec = confidenceOf(model).rec;
      x.conf = rec;
      if (was !== rec) note = "最低置信度已重置为 " + formatConfidence(rec) + "（" + (model?.name || "所选模型") + " 的推荐值）";
    });
    if (note) toast(note);
  }, { alignRight: true });
}

export function openConfidenceMenu(anchor: PopAnchor): void {
  const s = getState();
  const p = selectedPreset(s);
  if (p.builtin) return;
  const m = modelById(s.models, p.model);
  const c = confidenceOf(m);
  const cur = presetConfidence(p, s.models);
  const vals: number[] = [];
  for (let i = Math.round(c.min * 10); i <= 9; i++) vals.push(i / 10);
  openMenu(anchor, [
    { h: "最低置信度 · 低于此值不涂抹" },
    ...vals.map((v) => ({
      l: formatConfidence(v),
      s: v === c.rec ? "推荐 · 按 " + (m?.name || "所选模型") + " 校准" : v < c.rec ? "命中更多，误报增加" : "命中更少，可能漏检",
      k: v, on: cur === v,
    })),
  ], (it) => {
    if (cur === it.k) return;
    store.update((d) => { const x = presetOf(d); bumpVersion(x); x.conf = it.k as number; });
  }, { alignRight: true });
}

/** The rule-set picker stays open for multi-select: after a pick it opens again at the same place. */
export function openPresetRulesMenu(anchor: HTMLElement): void {
  const s = getState();
  const p = selectedPreset(s);
  openMenu(anchor, [
    { h: "规则集 · 可多选" },
    ...availableRulesets(s.rulesets, s.plugins).map((rs) => ({ l: rs.name, s: rs.ver, k: rs.id, on: p.rules.includes(rs.id) })),
    "-",
    { l: "管理规则", ic: "sliders", k: "_r" },
  ], (it) => {
    if (it.k === "_r") { openSubpage("presets", { presetTab: "rules" }); return; }
    const has = p.rules.includes(String(it.k));
    if (has && p.rules.length === 1) { toast("至少保留一个规则集"); return; }
    store.update((d) => {
      const x = presetOf(d);
      bumpVersion(x);
      x.rules = has ? x.rules.filter((r) => r !== it.k) : x.rules.concat(String(it.k));
    });
    if (anchor.isConnected) openPresetRulesMenu(anchor);
  }, { alignRight: true });
}

export function openPresetMoreMenu(anchor: PopAnchor): void {
  const p = selectedPreset(getState());
  openMenu(anchor, [
    ...(p.builtin ? [] : [{ l: "历史版本", ic: "clock", k: "hist" }]),
    { l: "导出", ic: "download", k: "exp" },
    ...(p.builtin ? [] : (["-", { l: "删除", ic: "trash", k: "del", d: true }] as const)),
  ], (it) => {
    if (it.k === "hist") { if (isLive()) void openVersions("preset"); else openModal({ type: "versions", kind: "preset" }); }
    else if (it.k === "del") {
      store.update((d) => {
        d.presets = d.presets.filter((x) => x.id !== p.id);
        d.presetSel = "office";
        if (d.homePreset === p.name) d.homePreset = d.presets[0]!.name;
      });
      toast("已删除");
    } else if (isLive()) void exportLive("preset", p.id);
    else toast("已导出（不含模型）");
  }, { alignRight: true });
}

/* ── Rule sets ────────────────────────────────────────────────────────────── */

export function selectRuleset(id: string): void {
  store.update((d) => { d.rulesetSel = id; });
}

export function newRuleset(): void {
  const id = nextId("rs");
  store.update((d) => { d.rulesets.push({ id, name: "新规则集", ver: "v1", rules: [] }); d.rulesetSel = id; });
}

export function copyRuleset(): void {
  store.update((d) => {
    const copy = duplicate(rulesetOf(d) as Ruleset, nextId("rs"));
    d.rulesets.push(copy);
    d.rulesetSel = copy.id;
  });
  toast("已复制");
}

export function toggleRule(index: number): void {
  store.update((d) => {
    const r = rulesetOf(d);
    bumpVersion(r);
    r.rules[index]!.on = !r.rules[index]!.on;
  });
}

export function openRulesetMoreMenu(anchor: PopAnchor): void {
  const s = getState();
  const r = selectedRuleset(s);
  const lock = !!(r.builtin || r.plugin);
  openMenu(anchor, [
    ...(lock ? [] : [{ l: "历史版本", ic: "clock", k: "hist" }]),
    { l: "导出", ic: "download", k: "exp" },
    ...(lock ? [] : (["-", { l: "删除", ic: "trash", k: "del", d: true }] as const)),
  ], (it) => {
    if (it.k === "hist") { if (isLive()) void openVersions("ruleset"); else openModal({ type: "versions", kind: "ruleset" }); return; }
    if (it.k === "exp") { if (isLive()) void exportLive("ruleset", r.id); else toast("已导出 " + r.name); return; }
    const used = getState().presets.filter((p) => p.rules.includes(r.id));
    if (used.length) { toast("正在被「" + used[0]!.name + "」使用"); return; }
    store.update((d) => { d.rulesets = d.rulesets.filter((x) => x.id !== r.id); d.rulesetSel = null; });
    toast("已删除");
  }, { alignRight: true });
}

export interface RuleDraft {
  name: string;
  kind: RuleKind;
  cat: CategoryId;
  bd: Boundary;
  body: string;
}
export type SaveRuleResult = "saved" | "unchanged" | "name" | "body" | "lookaround";

/** Validates and stores an edited or new rule. Returns what the dialog has to react to. */
export function saveRule(index: number | null, draft: RuleDraft): SaveRuleResult {
  if (!draft.name.trim()) return "name";
  if (!draft.body.trim()) return "body";
  // Go regex (RE2) has no lookaround; boundaries are a declared attribute instead.
  if (draft.kind === "正则" && /\(\?<?[=!]/.test(draft.body)) {
    toast("不支持前后断言（如 (?<!…)），请改用“前后边界”");
    return "lookaround";
  }
  const x: Rule = { name: draft.name.trim(), kind: draft.kind, cat: draft.cat, bd: draft.kind === "正则" ? draft.bd || "" : "", body: draft.body.trim(), on: true };
  const old = index != null ? selectedRuleset(getState()).rules[index] : undefined;
  if (old && (["name", "kind", "cat", "bd", "body"] as const).every((k) => (old[k] || "") === x[k])) {
    closeModal();
    toast("内容未变，版本不变");
    return "unchanged";
  }
  let ver = "";
  store.update((d) => {
    const r = rulesetOf(d);
    bumpVersion(r);
    if (index != null) Object.assign(r.rules[index]!, x, { n: 0, ecat: old && old.cat === x.cat ? old.ecat : undefined, rid: old?.rid });
    else r.rules.push(x);
    ver = r.ver;
  });
  closeModal();
  toast("已保存为 " + ver);
  return "saved";
}

export function deleteRule(index: number): void {
  let ver = "";
  store.update((d) => {
    const r = rulesetOf(d);
    bumpVersion(r);
    r.rules.splice(index, 1);
    ver = r.ver;
  });
  closeModal();
  toast("已删除 · 保存为 " + ver);
}

/* ── Versions ─────────────────────────────────────────────────────────────── */

export const versionTarget = (s: AppState, kind: "preset" | "ruleset"): Versioned => (kind === "ruleset" ? selectedRuleset(s) : selectedPreset(s));

/** Rolling back saves the chosen version's content as a new version; the other versions stay. */
export function rollback(kind: "preset" | "ruleset", index: number): void {
  if (isLive()) {
    void rollbackLive(kind, index).then((from) => { closeModal(); if (from) toast("已用 " + from + " 的内容保存为新版本"); });
    return;
  }
  let from = "";
  let ver = "";
  store.update((d) => {
    if (kind === "ruleset") {
      const o = rulesetOf(d);
      const h = o.hist![index]!;
      from = h.ver;
      const rules: Rule[] = JSON.parse(JSON.stringify(h.data.rules));
      bumpVersion(o);
      o.rules = rules;
      ver = o.ver;
    } else {
      const o = presetOf(d);
      const h = o.hist![index]!;
      from = h.ver;
      const data: import("../domain/types").PresetContent = JSON.parse(JSON.stringify(h.data));
      const avail = availableRulesets(d.rulesets, d.plugins);
      data.rules = data.rules.filter((id) => avail.some((x) => x.id === id));
      if (!data.rules.length) data.rules = [...o.rules];
      bumpVersion(o);
      Object.assign(o, data);
      ver = o.ver;
    }
  });
  closeModal();
  toast("已用 " + from + " 的内容保存为 " + ver);
}

/* ── Import ───────────────────────────────────────────────────────────────── */

const importSource = {
  preset: (): Pick<Preset, "name" | "det" | "model" | "conf" | "rules" | "cats" | "img"> => ({
    name: "简历脱敏", det: "model", model: "astr", conf: 0.5, rules: ["cn"], cats: allCategories<PresetTreatment>("mask"), img: "remove",
  }),
  ruleset: (): Pick<Ruleset, "name" | "rules"> => ({
    name: "企业词条", rules: [{ name: "项目代号", kind: "词表", cat: "other", body: "", n: 40, on: true }],
  }),
};

export function importNameClashes(kind: "preset" | "ruleset", name: string): boolean {
  const s = getState();
  return (kind === "ruleset" ? availableRulesets(s.rulesets, s.plugins) : s.presets).some((x) => x.name === name);
}

export function startImport(kind: "preset" | "ruleset"): void {
  if (isLive()) { void importLive(kind); return; }
  const name = importSource[kind]().name;
  if (!importNameClashes(kind, name)) finishImport(kind, name);
  else openModal({ type: "importConflict", kind, name });
}

export function finishImport(kind: "preset" | "ruleset", name: string): void {
  if (isLive()) { finishImportLive(kind, name); return; }
  const common = { name, ver: "v1", edited: "刚刚", hist: [] };
  store.update((d) => {
    if (kind === "ruleset") {
      const o: Ruleset = { ...importSource.ruleset(), ...common, id: nextId("rs") };
      d.rulesets.push(o);
      d.rulesetSel = o.id;
    } else {
      const o: Preset = { ...importSource.preset(), ...common, id: nextId("p") };
      d.presets.push(o);
      d.presetSel = o.id;
    }
  });
  closeModal();
  toast("已导入「" + name + "」 · v1");
}
