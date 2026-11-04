// Schemes (presets) and rule sets: editing, versions, import and the menus of the configuration pages.
import { MODEL_LABELS } from "../domain/catalog";
import {
  availableRulesets, bumpVersion, confidenceOf, duplicate, formatConfidence, losesNothing, modelById, presetConfidence, presetCategories, presetCategorySources, rulesetChangeLoss,
  type DetectionLoss, type Versioned,
} from "../domain/presets";
import type { CategoryId, Preset, PresetTab, PresetTreatment, Rule, RuleDraft, Ruleset } from "../domain/types";
import { getState, store, type AppState } from "../store";
import { closeModal, openMenu, openModal, toast, type ModalSpec, type PopAnchor } from "../overlay/store";
import { openSubpage } from "./navigation";
import { requestScroll } from "./scrollRequests";
import { nextId } from "./timers";
import { isLive } from "./live";
import { exportLive, finishImportLive, importLive, openVersions, rollbackLive } from "./liveConfig";
import type { Draft } from "immer";


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

export function setPresetTab(tab: PresetTab): void {
  store.update((d) => { d.presetTab = tab; });
}

/** What a link on the configuration page leads to: a scheme, a rule set or a model, and one of its categories. */
export type ConfigTarget = { tab: "scheme"; id: string } | { tab: "rules" | "models"; id: string; category?: CategoryId };

/** Shows the object a link names on its own tab, scrolled to the category when one is given. */
export function showInConfig(t: ConfigTarget): void {
  store.update((d) => {
    d.presetTab = t.tab;
    if (t.tab === "scheme") d.presetSel = t.id;
    else if (t.tab === "rules") d.rulesetSel = t.id;
    else d.modelSel = t.id;
  });
  if (t.tab !== "scheme" && t.category) requestScroll({ kind: "category", tab: t.tab, category: t.category });
}

export function selectModel(id: string): void {
  store.update((d) => { d.modelSel = id; });
}

/* ── Schemes ──────────────────────────────────────────────────────────────── */

export function selectPreset(id: string): void {
  store.update((d) => { d.presetSel = id; });
}

export function newPreset(): void {
  const id = nextId("p");
  store.update((d) => {
    // Start from the built-in scheme's rule sets: stored rule set ids differ from the demo's "cn".
    const rules = (d.presets.find((p) => p.builtin)?.rules ?? []).filter((r) => d.rulesets.some((x) => x.id === r));
    const fallback = d.rulesets.find((r) => r.builtin && !r.plugin) ?? d.rulesets[0];
    if (!rules.length && fallback) rules.push(fallback.id);
    d.presets.push({ id, name: "新方案", ver: "v1", det: "model", model: d.models[0]?.id ?? "astr", conf: 0.5, rules, cats: {}, img: "remove", edited: "刚刚" }); // a category without a setting is masked
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
    presetCategories(p as Preset, d.models, d.rulesets as Ruleset[], d.categories).forEach((t) => { if (p.cats[t] == null || p.cats[t] === "na") p.cats[t] = "mask"; });
    if (det === "model" && p.conf == null) p.conf = confidenceOf(modelById(d.models, p.model)).rec;
  });
}

/** Sets what the scheme does with a category; a category nothing of the scheme detects stays off. */
export function setCategoryTreatment(t: string, k: PresetTreatment): void {
  const s = getState();
  if (!presetCategorySources(selectedPreset(s), s.models, availableRulesets(s.rulesets, s.plugins)).has(t)) return;
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
    { h: "规则组 · 可多选" },
    // a rule set without rules (one being written) detects nothing and is not stored yet
    ...availableRulesets(s.rulesets, s.plugins).filter((rs) => rs.rules.length || p.rules.includes(rs.id))
      .map((rs) => ({ l: rs.name, s: rs.ver + " · " + rs.rules.length + " 条规则", k: rs.id, on: p.rules.includes(rs.id) })),
    "-",
    { l: "管理规则", ic: "sliders", k: "_r" },
  ], (it) => {
    if (it.k === "_r") { openSubpage("presets", { presetTab: "rules" }); return; }
    const has = p.rules.includes(String(it.k));
    if (has && p.rules.length === 1) { toast("至少保留一个规则组"); return; }
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
        if (d.homePreset === p.name) d.homePreset = d.presets[0]?.name ?? "";
        // the home page's scheme, which is there in the demo and live alike (ids differ between them)
        d.presetSel = (d.presets.find((x) => x.name === d.homePreset) ?? d.presets[0])?.id ?? "";
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
  store.update((d) => { d.rulesets.push({ id, name: "新规则组", ver: "v1", rules: [] }); d.rulesetSel = id; });
}

export function copyRuleset(): void {
  store.update((d) => {
    const copy = duplicate(rulesetOf(d) as Ruleset, nextId("rs"));
    d.rulesets.push(copy);
    d.rulesetSel = copy.id;
  });
  toast("已复制");
}

/** The confirmation that lists what a change would cost; cancelling closes it, or returns to `back`. */
function confirmLoss(action: string, loss: DetectionLoss, go: () => void, back?: ModalSpec): void {
  openModal({
    type: "detectionLoss", action,
    lost: loss.categories.map((x) => ({ preset: x.preset.name, categories: x.categories })),
    noRules: loss.noRules.map((p) => p.name),
    go: () => { closeModal(); go(); },
    back,
  });
}

/**
 * Changes the selected rule set with `edit` as one new version (one save of the whole set). When the same edit
 * on a copy would leave a scheme that uses the rule set without a detection source for a category it masks or
 * replaces, or without any rule switched on, the schemes are listed and the edit waits for a yes; cancelling
 * changes nothing. A rule set no scheme uses is changed at once.
 */
function editRuleset(action: string, edit: (r: Ruleset) => void, done?: (r: Ruleset) => void, back?: ModalSpec): void {
  const s = getState();
  const r = selectedRuleset(s);
  const apply = () => {
    store.update((d) => {
      const target = d.rulesets.find((x) => x.id === r.id);
      if (!target) return;
      bumpVersion(target);
      edit(target as Ruleset);
    });
    const target = getState().rulesets.find((x) => x.id === r.id);
    if (target) done?.(target);
  };
  const copy: Ruleset = { ...r, rules: r.rules.map((x) => ({ ...x })) };
  edit(copy);
  const loss = rulesetChangeLoss(s.presets, { rulesets: availableRulesets(s.rulesets, s.plugins), models: s.models }, copy);
  if (losesNothing(loss)) apply();
  else confirmLoss(action, loss, apply, back);
}

/** Switches one rule, addressed by its index in the rule set. */
export function toggleRule(index: number): void {
  const on = !selectedRuleset(getState()).rules[index]!.on;
  editRuleset(on ? "启用" : "停用", (r) => { r.rules[index]!.on = on; });
}

/**
 * Switches every rule of the selected rule set that reports `category` on or off: one new version, so one save of
 * the whole set. Rules are switched, never removed; nothing changes when all of them already are.
 */
export function setCategoryRulesOn(category: CategoryId, on: boolean): void {
  if (!selectedRuleset(getState()).rules.some((x) => x.cat === category && x.on !== on)) return;
  editRuleset(on ? "启用" : "停用", (r) => { r.rules.forEach((x) => { if (x.cat === category) x.on = on; }); });
}

/** Why the selected rule set cannot be deleted now (a scheme uses it), or null. */
export function rulesetDeleteBlocker(s: AppState, id: string): string | null {
  const used = s.presets.filter((p) => p.rules.includes(id));
  return used.length ? "被「" + used[0]!.name + "」" + (used.length > 1 ? "等 " + used.length + " 个方案" : "") + "使用" : null;
}

export function openRulesetMoreMenu(anchor: PopAnchor): void {
  const s = getState();
  const r = selectedRuleset(s);
  const lock = !!(r.builtin || r.plugin);
  const blocked = rulesetDeleteBlocker(s, r.id);
  openMenu(anchor, [
    ...(lock ? [] : [{ l: "历史版本", ic: "clock", k: "hist" }]),
    { l: "导出", ic: "download", k: "exp" },
    // a rule set a scheme uses stays: the item says why instead of failing once picked
    ...(lock ? [] : (["-", { l: "删除", ic: "trash", k: "del", d: !blocked, off: !!blocked, ...(blocked ? { s: blocked } : {}) }] as const)),
  ], (it) => {
    if (it.k === "hist") { if (isLive()) void openVersions("ruleset"); else openModal({ type: "versions", kind: "ruleset" }); return; }
    if (it.k === "exp") { if (isLive()) void exportLive("ruleset", r.id); else toast("已导出 " + r.name); return; }
    if (rulesetDeleteBlocker(getState(), r.id)) return;
    store.update((d) => { d.rulesets = d.rulesets.filter((x) => x.id !== r.id); d.rulesetSel = null; });
    toast("已删除");
  }, { alignRight: true });
}

export type { RuleDraft };
export type SaveRuleResult = "saved" | "unchanged" | "name" | "body" | "lookaround" | "confirm";

/**
 * Validates and stores an edited or new rule; `index` is the rule's index in the rule set (null adds one at the
 * end). Returns what the dialog has to react to ("confirm": the schemes it would cost are listed first).
 */
export function saveRule(index: number | null, draft: RuleDraft): SaveRuleResult {
  if (!draft.name.trim()) return "name";
  if (!draft.body.trim()) return "body";
  // Go regex (RE2) has no lookaround; boundaries are a declared attribute instead.
  if (draft.kind === "正则" && /\(\?<?[=!]/.test(draft.body)) {
    toast("不支持前后断言（如 (?<!…)），请改用“前后边界”");
    return "lookaround";
  }
  const rs = selectedRuleset(getState());
  const old = index != null ? rs.rules[index] : undefined;
  const x: Rule = { name: draft.name.trim(), kind: draft.kind, cat: draft.cat, bd: draft.kind === "正则" ? draft.bd || "" : "", body: draft.body.trim(), on: old?.on ?? true };
  if (old && (["name", "kind", "cat", "bd", "body"] as const).every((k) => (old[k] || "") === x[k])) {
    closeModal();
    toast("内容未变，版本不变");
    return "unchanged";
  }
  let applied = false;
  editRuleset("保存", (r) => {
    if (index != null) Object.assign(r.rules[index]!, x, { n: 0, rid: old?.rid });
    else r.rules.push({ ...x });
  }, (r) => {
    applied = true;
    closeModal();
    toast("已保存为 " + r.ver);
  }, { type: "ruleEditor", rulesetId: rs.id, index, draft });
  return applied ? "saved" : "confirm";
}

/** Deletes a rule, addressed by its index in the rule set. `draft` is what the editor holds, for going back to it. */
export function deleteRule(index: number, draft?: RuleDraft): void {
  const rs = selectedRuleset(getState());
  if (rs.rules.length === 1) { toast("规则组至少保留一条规则；不再需要时，可从「更多」删除整个规则组"); return; }
  editRuleset("删除", (r) => { r.rules.splice(index, 1); }, (r) => {
    closeModal();
    toast("已删除 · 保存为 " + r.ver);
  }, { type: "ruleEditor", rulesetId: rs.id, index, draft });
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
    name: "简历脱敏", det: "model", model: "astr", conf: 0.5, rules: ["cn"], cats: {}, img: "remove",
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
