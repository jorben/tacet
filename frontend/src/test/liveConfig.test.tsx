import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { copyPreset, newPreset, newRuleset, saveRule, selectPreset, selectRuleset, setCategoryTreatment, setDetection, rollback } from "../actions/presets";
import { installSamplePlugin, togglePlugin } from "../actions/plugins";
import { checkForUpdate, setSetting, setThemePref } from "../actions/settings";
import { downloadAccel, downloadModel } from "../actions/models";
import { exportLive, flushConfig, saveSettingsNow, importLive, loadConfig, openVersions, stopConfigSync } from "../actions/liveConfig";
import { takePaths } from "../actions/live";
import { runPresetTest, testErrorText } from "../actions/presetTest";
import { createCategory } from "../actions/categories";
import { resetModelMapping, setLabelCategory } from "../actions/modelMapping";
import { unhandledCategories } from "../features/presets/ModelMappingEditor";
import { overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import type { ConfigItem, EngineModelRuntime } from "../services/types";
import { getState, store } from "../store";
import { FakeEngine } from "./liveFake";

const item = (kind: string, id: string, name: string, content: unknown, readOnly = true): ConfigItem => ({ id, kind, name, note: "", version: 1, readOnly, content: JSON.stringify(content) });

const RULES = item("ruleset", "builtin-cn-office", "办公文档规则", { rules: [
  { id: "cn_mobile", category: "phone", pattern: "1[3-9][0-9]{9}", boundary: "digit" },
  { id: "email", category: "email", pattern: "[a-z]+@[a-z]+\\.cn" },
  { id: "cn_id", category: "id_number", pattern: "[0-9]{18}", validator: "cn_id" },
] });
const SCHEME = item("scheme", "builtin-scheme-model", "占位符方案（含模型类别）", { category_actions: { phone: "placeholder", email: "placeholder", id_number: "placeholder", person: "placeholder", address: "placeholder" }, image_default: "remove" });
const SCHEME_RO = item("scheme", "builtin-scheme-placeholder", "占位符方案", { category_actions: { phone: "placeholder", email: "placeholder", id_number: "placeholder" }, image_default: "remove" });
const MODEL = item("model_profile", "builtin-model-astrlink", "AstrLink Guard", { model_id: "astrlink-guard-int8", manifest_digest: "sha256:aa", backend: "cpu", adapter_floor: 0.5, calibrated: 0.8, label_map: { private_person: "person", private_address: "address", phone: "phone", email: "email" }, memory_bytes: 1 });
const P_MODEL = item("preset", "builtin-preset-model", "办公外发", { ruleset_id: "builtin-cn-office", scheme_id: "builtin-scheme-model", model_profile_id: "builtin-model-astrlink", mode: "guided", rules_only: false, min_confidence: 0.8, memory_bytes: 1073741824, dpi: 200, style: "solid", retention_days: 0 });

let fake: FakeEngine;
const writes = () => fake.calls.filter((c) => /^config(Create|Save|Delete|Rollback)/.test(c));
const toastText = () => overlayStore.get().toast?.msg ?? "";
const sleep = (ms: number) => act(async () => { await new Promise((r) => setTimeout(r, ms)); });

beforeEach(async () => {
  fake = new FakeEngine();
  fake.items = [RULES, SCHEME, SCHEME_RO, MODEL, P_MODEL].map((i) => ({ ...i }));
  setService({ ...mockService, engine: fake });
  await loadConfig();
});
afterEach(() => {
  stopConfigSync();
  setService(null);
});

describe("方案与规则集的存储同步", () => {
  it("启动时读出存储的内容；读出之后没有任何写入", async () => {
    const s = getState();
    expect(s.presets.map((p) => [p.name, p.det, p.builtin])).toEqual([["办公外发", "model", true]]);
    expect(s.homePreset).toBe("办公外发");
    expect(s.models).toHaveLength(1);
    expect(s.rulesets[0]).toMatchObject({ name: "办公文档规则", builtin: true, ver: "内建" });
    await sleep(700);
    expect(writes()).toEqual([]);
  });

  it("复制内建方案：存为新的方案对象和预设对象，原对象不动；之后改动保存为新版本", async () => {
    selectPreset("builtin-preset-model");
    act(() => copyPreset());
    await waitFor(() => expect(writes()).toEqual(["configCreate:scheme", "configCreate:preset"]));
    const copy = getState().presets.find((p) => !p.builtin)!;
    expect(copy.stored?.id).toMatch(/^cfg_/);
    expect(copy.id).toBe(copy.stored!.id);
    expect(copy.name).toBe("办公外发 副本");
    expect(getState().presetSel).toBe(copy.id);
    const stored = fake.items.find((i) => i.id === copy.id)!;
    expect(JSON.parse(stored.content)).toMatchObject({ ruleset_id: "builtin-cn-office", model_profile_id: "builtin-model-astrlink", rules_only: false, min_confidence: 0.8 });
    expect(fake.items.find((i) => i.id === "builtin-preset-model")!.version).toBe(1);
    // a change on the copy is one save of the scheme object
    act(() => setCategoryTreatment("phone", "mask"));
    await waitFor(() => expect(writes()).toHaveLength(4));
    // a change of the categories is a new version of the scheme and, with it, of the preset
    expect(writes().slice(2)).toEqual([expect.stringMatching(/^configSave:/), expect.stringMatching(/^configSave:/)]);
    expect(JSON.parse(fake.items.find((i) => i.id === copy.id)!.content).scheme_rev).toBe(2);
    const sch = fake.items.find((i) => i.id === JSON.parse(stored.content).scheme_id)!;
    expect(JSON.parse(sch.content).category_actions.phone).toBe("mask");
    expect(sch.version).toBe(2);
  });

  it("新建方案：沿用内建方案的规则集存储编号，存为新的方案对象和预设对象", async () => {
    act(() => newPreset());
    await waitFor(() => expect(writes()).toEqual(["configCreate:scheme", "configCreate:preset"]));
    const p = getState().presets.find((x) => x.name === "新方案")!;
    expect(p.stored?.id).toMatch(/^cfg_/);
    expect(JSON.parse(fake.items.find((i) => i.id === p.stored!.id)!.content)).toMatchObject({ ruleset_id: "builtin-cn-office", model_profile_id: "builtin-model-astrlink", rules_only: false });
  });

  it("新规则集与引用它的方案：先存规则集，方案里的编号换成存储编号", async () => {
    act(() => newRuleset());
    const draftId = getState().rulesetSel!;
    act(() => { saveRule(null, { name: "工号", kind: "正则", cat: "other", bd: "", body: "HZ[0-9]{6}" }); });
    await waitFor(() => expect(fake.calls).toContain("configCreate:ruleset"));
    await waitFor(() => expect(getState().rulesets.find((r) => r.name === "新规则集")?.stored).toBeTruthy());
    const rs = getState().rulesets.find((r) => r.name === "新规则集")!;
    expect(rs.id).not.toBe(draftId);
    expect(getState().rulesetSel).toBe(rs.id);
    expect(JSON.parse(fake.items.find((i) => i.id === rs.id)!.content).rules[0]).toMatchObject({ name: "工号", category: "other", pattern: "HZ[0-9]{6}" });
  });

  it("方案在规则集尚未存好时就引用了它：写入时换成存储编号", async () => {
    selectPreset("builtin-preset-model");
    act(() => {
      newRuleset();
      saveRule(null, { name: "工号", kind: "正则", cat: "other", bd: "", body: "HZ[0-9]{6}" });
      copyPreset();
      store.update((d) => { const p = d.presets.find((x) => !x.builtin)!; p.rules.push(d.rulesetSel!); });
    });
    const draft = getState().presets.find((p) => !p.builtin)!.rules[1]!;
    await waitFor(() => expect(getState().presets.find((p) => !p.builtin)?.stored).toBeTruthy());
    const rs = getState().rulesets.find((r) => r.name === "新规则集")!;
    expect(rs.id).not.toBe(draft);
    const p = getState().presets.find((x) => !x.builtin)!;
    expect(p.rules).toEqual(["builtin-cn-office", rs.id]);
    expect(JSON.parse(fake.items.find((i) => i.id === p.id)!.content).extra_ruleset_ids).toEqual([rs.id]);
    expect(fake.calls.indexOf("configCreate:ruleset")).toBeLessThan(fake.calls.indexOf("configCreate:preset"));
  });

  it("读出的自建方案不会因启动而被改写：规则集多检测的类别不自动加进方案", async () => {
    stopConfigSync();
    fake.items.push(item("ruleset", "cfg_r", "词条", { rules: [{ id: "a", name: "代号", category: "other", terms: ["甲"] }] }, false),
      item("scheme", "cfg_s", "方案 x", { category_actions: { phone: "placeholder" }, image_default: "remove" }, false),
      item("preset", "cfg_p", "自建", { ruleset_id: "builtin-cn-office", extra_ruleset_ids: ["cfg_r"], scheme_id: "cfg_s", mode: "guided", rules_only: true, memory_bytes: 1, dpi: 200, style: "solid", retention_days: 0 }, false));
    await loadConfig();
    expect(getState().presets.find((p) => p.name === "自建")).toMatchObject({ rules: ["builtin-cn-office", "cfg_r"], builtin: false });
    await sleep(700);
    expect(writes()).toEqual([]);
    // an unrelated change makes the sync run; the stored scheme of the untouched one is left as it is
    act(() => newRuleset());
    await waitFor(() => expect(fake.calls).toContain("configCreate:ruleset"));
    await sleep(500);
    expect(writes()).toEqual(["configCreate:ruleset"]);
    // Editing another category keeps an unselected category disabled.
    selectPreset("cfg_p");
    act(() => setCategoryTreatment("email", "mask"));
    await waitFor(() => expect(fake.calls).toContain("configSave:cfg_s"));
    const actions = JSON.parse(fake.items.find((i) => i.id === "cfg_s")!.content).category_actions;
    expect(actions).toMatchObject({ email: "mask" });
    expect(actions).not.toHaveProperty("other");
  });

  it("停用一条规则保存为停用，不删除", async () => {
    act(() => newRuleset());
    act(() => { saveRule(null, { name: "甲", kind: "词表", cat: "name", bd: "", body: "张三\n李四" }); });
    await waitFor(() => expect(getState().rulesets.find((r) => r.name === "新规则集")?.stored).toBeTruthy());
    const id = getState().rulesets.find((r) => r.name === "新规则集")!.id;
    selectRuleset(id);
    const { toggleRule } = await import("../actions/presets");
    act(() => toggleRule(0));
    await waitFor(() => expect(JSON.parse(fake.items.find((i) => i.id === id)!.content).rules[0].disabled).toBe(true));
    expect(JSON.parse(fake.items.find((i) => i.id === id)!.content).rules[0].terms).toEqual(["张三", "李四"]);
  });

  it("在屏幕上删除方案：先删预设再删方案对象；删除规则集在方案之后", async () => {
    selectPreset("builtin-preset-model");
    act(() => copyPreset());
    await waitFor(() => expect(getState().presets.find((p) => !p.builtin)?.stored).toBeTruthy());
    const id = getState().presets.find((p) => !p.builtin)!.id;
    const schemeId = JSON.parse(fake.items.find((i) => i.id === id)!.content).scheme_id;
    act(() => store.update((d) => { d.presets = d.presets.filter((p) => p.id !== id); }));
    await waitFor(() => expect(fake.calls).toContain("configDelete:" + schemeId));
    expect(fake.calls.filter((c) => c.startsWith("configDelete"))).toEqual(["configDelete:" + id, "configDelete:" + schemeId]);
    expect(fake.items.some((i) => i.id === id || i.id === schemeId)).toBe(false);
  });

  it("内建方案与规则集改了也不会写入", async () => {
    act(() => store.update((d) => { d.presets[0]!.conf = 0.6; d.rulesets[0]!.name = "改名"; }));
    await sleep(700);
    expect(writes()).toEqual([]);
  });

  it("存储拒绝时提示原因并按存储的内容重新载入屏幕", async () => {
    act(() => newRuleset());
    await waitFor(() => expect(fake.calls).toContain("configCreate:ruleset"));
    fake.configFails = new Error("CONFIG_NAME_CONFLICT");
    act(() => copyPreset());
    await waitFor(() => expect(toastText()).toContain("名称已被使用"));
    await waitFor(() => expect(getState().presets.every((p) => p.builtin || p.stored)).toBe(true));
    fake.configFails = null;
    await sleep(500);
    // the copy that could not be stored is gone from the screen again
    expect(getState().presets.filter((p) => !p.builtin)).toHaveLength(0);
  });

  it("没有任何规则或模型检测的类别提示后不写入", async () => {
    selectPreset("builtin-preset-model");
    act(() => copyPreset());
    await waitFor(() => expect(getState().presets.find((p) => !p.builtin)?.stored).toBeTruthy());
    act(() => setDetection("rules"));
    await flushConfig();
    act(() => setCategoryTreatment("addr", "mask"));
    await waitFor(() => expect(toastText()).toContain("地址"));
    const id = getState().presets.find((p) => !p.builtin)!.id;
    const scheme = fake.items.find((i) => i.id === JSON.parse(fake.items.find((x) => x.id === id)!.content).scheme_id)!;
    expect(JSON.parse(scheme.content).category_actions.address).toBeUndefined();
  });

  it("历史版本来自存储；回滚用所选版本保存为新版本", async () => {
    act(() => newRuleset());
    act(() => { saveRule(null, { name: "甲", kind: "词表", cat: "name", bd: "", body: "张三" }); });
    await waitFor(() => expect(getState().rulesets.find((r) => r.name === "新规则集")?.stored).toBeTruthy());
    const id = getState().rulesets.find((r) => r.name === "新规则集")!.id;
    const v1 = fake.items.find((i) => i.id === id)!.content;
    selectRuleset(id);
    act(() => { saveRule(0, { name: "甲", kind: "词表", cat: "name", bd: "", body: "张三\n李四" }); });
    await waitFor(() => expect(fake.items.find((i) => i.id === id)!.version).toBe(2));
    fake.history[id] = [{ v: { number: 2, savedAt: Math.floor(Date.now() / 1000) }, content: fake.items.find((i) => i.id === id)!.content }, { v: { number: 1, savedAt: Math.floor(Date.now() / 1000) - 7200 }, content: v1 }];
    await openVersions("ruleset");
    expect(overlayStore.get().modal).toMatchObject({ type: "versions", kind: "ruleset" });
    const target = getState().rulesets.find((r) => r.id === id)!;
    expect(target.hist!.map((h) => [h.ver, h.when])).toEqual([["v1", "2 小时前"]]);
    rollback("ruleset", 0);
    await waitFor(() => expect(fake.calls).toContain("configRollback:" + id + ":1"));
    await waitFor(() => expect(getState().rulesets.find((r) => r.id === id)!.ver).toBe("v3"));
    expect(getState().rulesets.find((r) => r.id === id)!.rules[0]!.body).toBe("张三");
  });

  // A rules-only scheme stored by an earlier version still names a model: the screen shows it as rules-only and would
  // write it without the binding. Opening its history must not write anything.
  const legacyPreset = async () => {
    stopConfigSync();
    fake.items.push(item("scheme", "cfg_s", "方案 x", { category_actions: { phone: "placeholder" }, image_default: "remove" }, false),
      item("preset", "cfg_p", "旧方案", { ruleset_id: "builtin-cn-office", scheme_id: "cfg_s", model_profile_id: "builtin-model-astrlink", mode: "guided", rules_only: true, memory_bytes: 1, dpi: 200, style: "solid", retention_days: 0 }, false));
    await loadConfig();
    expect(getState().presets.find((p) => p.id === "cfg_p")).toMatchObject({ det: "rules", builtin: false });
    await sleep(500);
    expect(writes()).toEqual([]);
    selectPreset("cfg_p");
    fake.history.cfg_p = [{ v: { number: 1, savedAt: Math.floor(Date.now() / 1000) }, content: fake.items.find((i) => i.id === "cfg_p")!.content }];
  };

  it("打开方案的历史版本不重写方案：存储版本不变，仅规则的旧方案保留模型绑定", async () => {
    await legacyPreset();
    await act(async () => { await openVersions("preset"); });
    expect(overlayStore.get().modal).toMatchObject({ type: "versions", kind: "preset" });
    expect(getState().presets.find((p) => p.id === "cfg_p")!.edited).toBe("刚刚");
    await sleep(500);
    await act(async () => { await flushConfig(); });
    const stored = fake.items.find((i) => i.id === "cfg_p")!;
    expect(stored.version).toBe(1);
    expect(JSON.parse(stored.content)).toHaveProperty("model_profile_id", "builtin-model-astrlink");
    expect(writes()).toEqual([]);
    // a real change after that is still saved
    act(() => setCategoryTreatment("email", "mask"));
    await waitFor(() => expect(fake.calls).toContain("configSave:cfg_s"));
  });

  it("打开历史版本期间改了方案：改动照常保存，不被当成与存储一致", async () => {
    await legacyPreset();
    let release!: () => void;
    let asked = false;
    const gate = new Promise<void>((r) => { release = r; });
    fake.configHistory = async (id: string) => { asked = true; await gate; return (fake.history[id] ?? []).map((h) => h.v); };
    const opening = openVersions("preset");
    await waitFor(() => expect(asked).toBe(true));
    act(() => setCategoryTreatment("email", "mask"));
    release();
    await act(async () => { await opening; });
    expect(overlayStore.get().modal).toMatchObject({ type: "versions", kind: "preset" });
    await sleep(500);
    await act(async () => { await flushConfig(); });
    expect(JSON.parse(fake.items.find((i) => i.id === "cfg_s")!.content).category_actions).toMatchObject({ email: "mask" });
  });

  it("打开普通方案的历史版本不写入：存储版本和内容不变", async () => {
    stopConfigSync();
    // a model scheme stored exactly as the screen writes it
    const scheme = { category_actions: { phone: "placeholder" }, image_default: "remove" };
    const preset = { ruleset_id: "builtin-cn-office", scheme_id: "cfg_s", mode: "guided", rules_only: false, memory_bytes: 1073741824, dpi: 200, style: "solid", retention_days: 0, scheme_rev: 1, model_profile_id: "builtin-model-astrlink", min_confidence: 0.8 };
    fake.items.push(item("scheme", "cfg_s", "方案 y", scheme, false), item("preset", "cfg_p", "普通方案", preset, false));
    await loadConfig();
    expect(getState().presets.find((p) => p.id === "cfg_p")).toMatchObject({ det: "model", builtin: false });
    await sleep(500);
    expect(writes()).toEqual([]);
    selectPreset("cfg_p");
    fake.history.cfg_p = [{ v: { number: 1, savedAt: Math.floor(Date.now() / 1000) }, content: JSON.stringify(preset) }];
    await act(async () => { await openVersions("preset"); });
    expect(overlayStore.get().modal).toMatchObject({ type: "versions", kind: "preset" });
    expect(getState().presets.find((p) => p.id === "cfg_p")!.edited).toBe("刚刚");
    await sleep(500);
    await act(async () => { await flushConfig(); });
    for (const [id, content] of [["cfg_p", preset], ["cfg_s", scheme]] as const) {
      const stored = fake.items.find((i) => i.id === id)!;
      expect(stored.version).toBe(1);
      expect(stored.content).toBe(JSON.stringify(content));
    }
    expect(writes()).toEqual([]);
  });

  it("打开规则集的历史版本不重写规则集：存储版本不变", async () => {
    stopConfigSync();
    // stored in a form the screen would write differently (a name equal to the rule's id is left out)
    const content = { rules: [{ id: "a", name: "a", category: "other", terms: ["甲"] }] };
    fake.items.push(item("ruleset", "cfg_r", "词条", content, false));
    await loadConfig();
    await sleep(500);
    expect(writes()).toEqual([]);
    selectRuleset("cfg_r");
    fake.history.cfg_r = [{ v: { number: 1, savedAt: Math.floor(Date.now() / 1000) }, content: JSON.stringify(content) }];
    await act(async () => { await openVersions("ruleset"); });
    expect(overlayStore.get().modal).toMatchObject({ type: "versions", kind: "ruleset" });
    await sleep(500);
    await act(async () => { await flushConfig(); });
    const stored = fake.items.find((i) => i.id === "cfg_r")!;
    expect(stored.version).toBe(1);
    expect(JSON.parse(stored.content)).toEqual(content);
    expect(writes()).toEqual([]);
  });

  it("导出写文件（不含模型）；导入读文件，名称相同时先让人改名，不覆盖", async () => {
    await exportLive("preset", "builtin-preset-model");
    expect(fake.saved!.name).toBe("办公外发.json");
    expect(fake.saved!.text).not.toContain("builtin-model-astrlink");
    expect(JSON.parse(fake.saved!.text)).toMatchObject({ kind: "scheme", rulesets: ["办公文档规则"], detection: "model" });
    // the same file comes back: the name is taken
    fake.openText = fake.saved!.text;
    await importLive("preset");
    expect(overlayStore.get().modal).toMatchObject({ type: "importConflict", kind: "preset" });
    expect(getState().presets).toHaveLength(1);
    const { finishImport } = await import("../actions/presets");
    act(() => finishImport("preset", "我的版本"));
    await waitFor(() => expect(getState().presets.find((p) => p.name === "我的版本")?.stored).toBeTruthy());
    expect(getState().presets.find((p) => p.name === "我的版本")).toMatchObject({ det: "model", ver: "v1" });
    // a file that is not ours changes nothing
    fake.openText = "{";
    await importLive("preset");
    expect(toastText()).toContain("不是 Tacet 的配置文件");
    expect(getState().presets).toHaveLength(2);
    fake.openText = "";
    await importLive("ruleset"); // cancelled
    expect(getState().rulesets).toHaveLength(1);
  });

  it("自建类别：由内核保存并重新读出目录；导出的规则集带上类别定义，导入时同名沿用、其余新建并改写编号", async () => {
    const made = await createCategory("项目代号");
    if (!("id" in made)) throw new Error(JSON.stringify(made));
    expect(fake.calls).toContain("configCreate:category");
    expect(fake.items.find((i) => i.id === made.id)).toMatchObject({ kind: "category", name: "项目代号", content: JSON.stringify({ placeholder: "[项目代号]" }) });
    expect(getState().categories.at(-1)).toMatchObject({ id: made.id, name: "项目代号", origin: "custom", version: 1 });
    expect(await createCategory("电话")).toEqual({ error: "taken" });
    // a rule set reporting it is exported with the category's definition
    act(() => newRuleset());
    act(() => saveRule(null, { name: "代号", kind: "词表", cat: made.id, bd: "", body: "北斗" }));
    await flushConfig();
    const rs = getState().rulesets.find((r) => r.rules.some((x) => x.cat === made.id))!;
    await exportLive("ruleset", rs.id);
    const file = JSON.parse(fake.saved!.text);
    expect(file.categories).toEqual([{ id: made.id, name: "项目代号", placeholder: "[项目代号]" }]);
    // another workspace's file: its 项目代号 is ours by name, its 客户代号 is new, its ids are rewritten
    fake.openText = JSON.stringify({ ...file, name: "外来词条", categories: [...file.categories.map((c: object) => ({ ...c, id: "cfg_far1" })), { id: "cfg_far2", name: "客户代号", placeholder: "[客户]" }],
      rules: [{ id: "a", category: "cfg_far1", terms: ["北斗"] }, { id: "b", category: "cfg_far2", terms: ["甲方"] }] });
    await importLive("ruleset");
    const client = getState().categories.find((c) => c.name === "客户代号")!;
    expect(client).toMatchObject({ placeholder: "[客户]", origin: "custom" });
    const imported = getState().rulesets.find((r) => r.name === "外来词条")!;
    expect(imported.rules.map((r) => r.cat)).toEqual([made.id, client.id]);
  });

  it("模型映射：首次改动新建映射对象，再改保存新版本，恢复默认删除；内建方案对新类别为关闭并给出提示", async () => {
    const model = () => getState().models[0]!;
    expect(model().labels).toEqual({ private_person: "person", private_address: "address", phone: "phone", email: "email" });
    await setLabelCategory(model().id, "private_person", "organization");
    expect(fake.calls).toContain("configCreate:model_mapping");
    const stored = fake.items.find((i) => i.kind === "model_mapping")!;
    expect(stored.name).toBe("astrlink-guard-int8");
    expect(JSON.parse(stored.content)).toEqual({ model_id: "astrlink-guard-int8", label_map: { private_person: "organization", private_address: "address", phone: "phone", email: "email" } });
    expect(model().categories).toEqual(["address", "email", "organization", "phone"]);
    // the read-only office scheme stores no action for it: off, and the models tab says so
    const office = getState().presets.find((p) => p.builtin)!;
    expect(office.cats.organization).toBe("off");
    expect(unhandledCategories(office, model().categories!)).toEqual(["organization"]);
    await setLabelCategory(model().id, "email", null);
    expect(fake.calls).toContain("configSave:" + stored.id);
    expect(JSON.parse(fake.items.find((i) => i.id === stored.id)!.content).label_map).not.toHaveProperty("email");
    // a restart reads the stored mapping
    await loadConfig();
    expect(model().mapping?.stored).toEqual({ id: stored.id, version: 2 });
    expect(model().categories).toEqual(["address", "organization", "phone"]);
    await resetModelMapping(model().id);
    expect(fake.calls).toContain("configDelete:" + stored.id);
    expect(fake.items.some((i) => i.kind === "model_mapping")).toBe(false);
    expect(model().mapping).toBeUndefined();
    expect(model().categories).toEqual(["address", "email", "person", "phone"]);
  });

  it("批次开始时也带上所选方案的存储编号", async () => {
    takePaths(["/d/a.txt", "/d/b.txt"]);
    await waitFor(() => expect(getState().batch?.live?.id).toBe("b0001"));
    const { startBatch } = await import("../actions/batch");
    act(() => startBatch());
    await waitFor(() => expect(fake.calls).toContain("start"));
    expect(fake.started!.preset).toBe("builtin-preset-model");
    expect(fake.importedPreset).toBe("builtin-preset-model");
  });

  it("处理文件时用所选方案的存储编号；方案尚未存好会先存", async () => {
    selectPreset("builtin-preset-model");
    act(() => copyPreset());
    const name = getState().presets.find((p) => !p.builtin)!.name;
    act(() => store.update((d) => { d.homePreset = name; }));
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    expect(fake.calls.indexOf("configCreate:preset")).toBeLessThan(fake.calls.indexOf("import:quick:1"));
    const copy = getState().presets.find((p) => !p.builtin)!;
    expect(fake.importedPreset).toBe(copy.stored!.id);
    expect(fake.started!.preset).toBe(copy.stored!.id);
  });

  it("设置：启动读出；每项改动防抖后写回，写回的是整份当前设置", async () => {
    fake.stored = { closeBehavior: "quit", theme: "dark", redactStyle: "plaid", resourceTier: "std", retentionDays: 0, autoUpdate: true };
    await loadConfig();
    const s = getState();
    expect(s.settings).toEqual({ redact: "plaid", tier: "std", autoUpd: true, keep: "0", closeAct: "quit", gpu: true });
    expect(s.themePref).toBe("dark");
    expect(s.theme).toBe("dark");
    await sleep(500);
    expect(fake.calls).not.toContain("saveSettings");
    act(() => { setSetting("keep", "30"); setSetting("tier", "comfort"); setThemePref("light"); });
    await waitFor(() => expect(fake.calls).toContain("saveSettings"));
    expect(fake.calls.filter((c) => c === "saveSettings")).toHaveLength(1);
    expect(fake.stored).toEqual({ closeBehavior: "quit", theme: "light", redactStyle: "plaid", resourceTier: "comfort", retentionDays: 30, autoUpdate: true, gpuAcceleration: true });
  });

  it("插件页显示真实登记的内建插件；第三方安装、停用内建、检查更新都如实说明不可用，不做模拟", async () => {
    fake.pluginList = [
      { id: "tacet.docx", version: "0.1.0", roles: ["format"], formats: ["docx"], builtin: true, enabled: true },
      { id: "tacet.astrlink", version: "0.1.0", roles: ["detect"], formats: null, builtin: true, enabled: true },
      { id: "tacet.text", version: "0.1.0", roles: ["format"], formats: ["md", "txt"], builtin: true, enabled: true },
    ];
    await loadConfig();
    expect(getState().plugins.map((p) => [p.id, p.name, p.fmt, p.role])).toEqual([["tacet.docx", "DOCX", ".docx", "格式"], ["tacet.text", "文本 · Markdown · 日志", ".md .txt", "格式"]]);
    expect(getState().plugins.every((p) => p.builtin && p.on)).toBe(true);
    act(() => togglePlugin("tacet.docx"));
    expect(toastText()).toContain("不能停用");
    expect(getState().plugins[0]!.on).toBe(true);
    act(() => installSamplePlugin());
    expect(toastText()).toContain("沙箱");
    expect(getState().plugins).toHaveLength(2);
    act(() => checkForUpdate());
    expect(toastText()).toContain("更新通道尚未配置");
    expect(getState().update.status).toBe("idle");
  });

  const RT = (over: Partial<EngineModelRuntime> = {}): EngineModelRuntime => ({
    id: "astrlink-guard-int8", build: "astrlink-guard-int8", name: "AstrLink Guard", version: "0.1.3", variant: "INT8", languages: "中文 / 中英混排",
    preset: "builtin-preset-model", state: "ready", code: "", bytes: 38961301, downloaded: 38961301, minMemory: 0, eligible: true,
    dir: "/Users/x/Library/Application Support/Tacet/models/installed/astrlink-guard_0.1.3_0123", backend: "CPUExecutionProvider", device: "cpu",
    repository: "QuantumNous/astrlink-guard", accelerable: false, accelerated: false, ...over,
  });
  const PF = item("model_profile", "builtin-model-privacy-filter", "OpenAI Privacy Filter（固定版本）", { model_id: "privacy-filter-int8", manifest_digest: "sha256:bb", backend: "CPUExecutionProvider", adapter_floor: 0.2, calibrated: 0.5, label_map: { private_person: "person", private_email: "email" }, memory_bytes: 3 * 2 ** 30 });
  const P_PF = item("preset", "builtin-preset-privacy-filter", "办公外发（规则 + Privacy Filter）", { ruleset_id: "builtin-cn-office", scheme_id: "builtin-scheme-model", model_profile_id: "builtin-model-privacy-filter", mode: "guided", rules_only: false, min_confidence: 0.5, memory_bytes: 4 * 2 ** 30, dpi: 200, style: "solid", retention_days: 0 });
  const PF_RT = RT({ id: "privacy-filter-int8", build: "privacy-filter-int8", name: "OpenAI Privacy Filter", version: "7ffa9a0", languages: "英文为主", preset: "builtin-preset-privacy-filter", state: "pending", downloaded: 0, bytes: 1645000000, dir: "", minMemory: 16 * 2 ** 30, repository: "openai/privacy-filter" });

  it("模型来自内核的目录：顺序、名称、状态、进度与存储位置；没有模型时不查询", async () => {
    fake.items.push({ ...PF }, { ...P_PF });
    fake.modelList = [RT(), { ...PF_RT, state: "downloading", downloaded: 411250000 }];
    await loadConfig();
    const [a, p] = getState().models;
    expect(a).toMatchObject({ id: "astrlink-guard-int8", name: "AstrLink Guard", status: "ready", weightMiB: 37, runtime: { dir: fake.modelList[0]!.dir, device: "cpu", repository: "QuantumNous/astrlink-guard" } });
    expect(p).toMatchObject({ id: "privacy-filter-int8", name: "OpenAI Privacy Filter", languages: "英文为主", status: "downloading", progress: 25, minMemoryGiB: 16, runtime: { dir: "" } });
    // each scheme is bound to its own model
    expect(getState().presets.find((x) => x.stored?.id === "builtin-preset-privacy-filter")!.model).toBe("privacy-filter-int8");
    expect(getState().presets.find((x) => x.stored?.id === "builtin-preset-model")!.model).toBe("astrlink-guard-int8");
    fake.items = fake.items.filter((i) => i.kind !== "model_profile");
    fake.calls = [];
    await loadConfig();
    expect(fake.calls).not.toContain("models");
  });

  it("默认模型尚未下载时仍保留办公外发方案的模型绑定", async () => {
    fake.modelList = [RT({ state: "pending", downloaded: 0, dir: "" })];
    await loadConfig();
    expect(getState().models[0]!.status).toBe("missing");
    expect(getState().homePreset).toBe("办公外发");
  });

  it("下载交给内核；轮询进度直到就绪并提示", async () => {
    fake.items.push({ ...PF }, { ...P_PF });
    fake.modelList = [RT(), { ...PF_RT, minMemory: 0 }];
    await loadConfig();
    act(() => downloadModel("privacy-filter-int8"));
    await waitFor(() => expect(fake.calls).toContain("downloadModel:privacy-filter-int8"));
    await waitFor(() => expect(getState().models[1]!.status).toBe("downloading"));
    fake.modelList[1]!.downloaded = 822500000;
    await waitFor(() => expect(getState().models[1]!.progress).toBe(50), { timeout: 2000 });
    Object.assign(fake.modelList[1]!, { state: "ready", downloaded: fake.modelList[1]!.bytes, dir: "/m/installed/privacy-filter_7ffa9a0_aa" });
    await waitFor(() => expect(getState().models[1]!.status).toBe("ready"), { timeout: 2000 });
    expect(getState().models[1]!.runtime!.dir).toBe("/m/installed/privacy-filter_7ffa9a0_aa");
    expect(toastText()).toContain("OpenAI Privacy Filter 已就绪");
  });

  it("本机内存不足：不发起下载，说明需要的内存", async () => {
    fake.items.push({ ...PF }, { ...P_PF });
    fake.modelList = [RT(), { ...PF_RT, eligible: false }];
    await loadConfig();
    act(() => downloadModel("privacy-filter-int8"));
    expect(toastText()).toContain("16 GiB");
    await sleep(50);
    expect(fake.calls.filter((c) => c.startsWith("downloadModel"))).toEqual([]);
  });

  it("校验未通过后重新下载：先丢弃残留再下载；内核拒绝时说明原因", async () => {
    fake.modelList = [RT({ state: "error", code: "MODEL_VERIFY_FAILED", dir: "" })];
    await loadConfig();
    expect(getState().models[0]).toMatchObject({ status: "error", error: "integrity" });
    act(() => downloadModel("astrlink-guard-int8"));
    await waitFor(() => expect(fake.calls).toContain("downloadModel:astrlink-guard-int8"));
    expect(fake.calls.indexOf("discardModel:astrlink-guard-int8")).toBeLessThan(fake.calls.indexOf("downloadModel:astrlink-guard-int8"));
    fake.modelList[0]!.state = "paused";
    await loadConfig();
    fake.modelFails = new Error("MODEL_BUSY");
    act(() => downloadModel("astrlink-guard-int8"));
    await waitFor(() => expect(toastText()).toContain("请先完成或停止当前文档处理"));
  });

  it("方案改用更大的模型时，内存预算随之提高", async () => {
    fake.items.push({ ...PF }, { ...P_PF });
    fake.modelList = [RT(), { ...PF_RT, state: "ready" }];
    await loadConfig();
    selectPreset("builtin-preset-model");
    act(() => copyPreset());
    await waitFor(() => expect(writes()).toEqual(["configCreate:scheme", "configCreate:preset"]));
    const copy = getState().presets.find((p) => !p.builtin)!;
    act(() => store.update((d) => { d.presets.find((p) => p.id === copy.id)!.model = "privacy-filter-int8"; }));
    await waitFor(() => expect(JSON.parse(fake.items.find((i) => i.id === copy.id)!.content).model_profile_id).toBe("builtin-model-privacy-filter"));
    expect(JSON.parse(fake.items.find((i) => i.id === copy.id)!.content).memory_bytes).toBeGreaterThanOrEqual(3 * 2 ** 30);
  });

  const ACCEL = { id: "astrlink-guard-fp16", variant: "FP16", backend: "CoreMLExecutionProvider", state: "pending", code: "", bytes: 75451868, downloaded: 0, dir: "" };

  it("GPU 加速只是一项设置：随设置写回，不下载也不试运行；GPU 版本在模型卡里单独下载", async () => {
    fake.modelList = [RT({ accelerable: true, accel: { ...ACCEL } })];
    await loadConfig();
    expect(getState().settings.gpu).toBe(true);
    expect(getState().models[0]!.runtime!.accel).toMatchObject({ id: "astrlink-guard-fp16", variant: "FP16", status: "missing", weightMiB: 72 });
    act(() => setSetting("gpu", true));
    await waitFor(() => expect(fake.stored.gpuAcceleration).toBe(true));
    expect(fake.calls.filter((c) => c.startsWith("downloadModel") || c.startsWith("prepareModel"))).toEqual([]);
    act(() => downloadAccel("astrlink-guard-int8"));
    await waitFor(() => expect(fake.calls).toContain("downloadModel:astrlink-guard-fp16"));
    await waitFor(() => expect(getState().models[0]!.runtime!.accel!.status).toBe("downloading"));
    Object.assign(fake.modelList[0]!.accel!, { state: "ready", downloaded: ACCEL.bytes, dir: "/m/installed/astrlink-guard_0.1.3_ade3" });
    Object.assign(fake.modelList[0]!, { accelerated: true, device: "gpu", build: "astrlink-guard-fp16", dir: "/m/installed/astrlink-guard_0.1.3_ade3" });
    await waitFor(() => expect(getState().models[0]!.runtime!.accel!.status).toBe("ready"), { timeout: 2000 });
    expect(getState().models[0]!.runtime).toMatchObject({ accelerated: true, device: "gpu" });
    expect(toastText()).toContain("AstrLink Guard GPU 版本 已就绪");
    // the model itself was never touched
    expect(fake.calls).not.toContain("downloadModel:astrlink-guard-int8");
  });

  const modelReads = () => fake.calls.filter((c) => c === "models").length;

  it("启动时确认推理设备：只等检测时放慢刷新，确认后得到 GPU 结论并停止刷新", async () => {
    vi.useFakeTimers();
    try {
      fake.modelList = [RT({ accelerable: true, backend: "auto", device: "auto", detecting: true })];
      await loadConfig();
      expect(getState().models[0]!.runtime).toMatchObject({ device: "auto", detecting: true });
      const n = modelReads();
      await act(() => vi.advanceTimersByTimeAsync(700));
      expect(modelReads()).toBe(n); // nothing but the probe to wait for: the slower interval
      Object.assign(fake.modelList[0]!, { backend: "CoreMLExecutionProvider", device: "gpu", accelerated: true, detecting: false });
      await act(() => vi.advanceTimersByTimeAsync(1300));
      expect(getState().models[0]!.runtime).toMatchObject({ device: "gpu", backend: "CoreMLExecutionProvider", detecting: false });
      const settled = modelReads();
      await act(() => vi.advanceTimersByTimeAsync(10000));
      expect(modelReads()).toBe(settled);
    } finally {
      vi.useRealTimers();
    }
  });

  it("检测期间读取失败一次：仍继续刷新，直到得到结论（检测未完成带原因码）", async () => {
    vi.useFakeTimers();
    try {
      fake.modelList = [RT({ accelerable: true, backend: "auto", device: "auto", detecting: true })];
      await loadConfig();
      const read = fake.models.bind(fake);
      let failed = false;
      fake.models = async () => {
        if (failed) return read();
        failed = true;
        fake.calls.push("models");
        throw new Error("ENGINE_BUSY");
      };
      await act(() => vi.advanceTimersByTimeAsync(2000));
      expect(failed).toBe(true);
      expect(getState().models[0]!.runtime).toMatchObject({ device: "auto", detecting: true });
      Object.assign(fake.modelList[0]!, { detecting: false, deviceCode: "PLUGIN_EXITED_EARLY" });
      await act(() => vi.advanceTimersByTimeAsync(4000)); // after a failed read the wait doubles
      expect(getState().models[0]!.runtime).toMatchObject({ device: "auto", detecting: false, deviceCode: "PLUGIN_EXITED_EARLY" });
      const settled = modelReads();
      await act(() => vi.advanceTimersByTimeAsync(10000));
      expect(modelReads()).toBe(settled);
    } finally {
      vi.useRealTimers();
    }
  });

  it("检测期间读取一直失败：重试间隔逐次加倍，最长 30 秒；恢复后回到正常间隔", async () => {
    vi.useFakeTimers();
    try {
      fake.modelList = [RT({ accelerable: true, backend: "auto", device: "auto", detecting: true })];
      await loadConfig();
      const read = fake.models.bind(fake);
      let failing = true;
      fake.models = async () => {
        if (!failing) return read();
        fake.calls.push("models");
        throw new Error("ENGINE_BUSY");
      };
      const n = modelReads();
      // 2s, then 4s, 8s, 16s, 30s, 30s after each failure
      for (const wait of [2000, 4000, 8000, 16000, 30000, 30000]) {
        await act(() => vi.advanceTimersByTimeAsync(wait - 1));
        const before = modelReads();
        await act(() => vi.advanceTimersByTimeAsync(1));
        expect(modelReads()).toBe(before + 1);
      }
      expect(modelReads()).toBe(n + 6);
      failing = false;
      await act(() => vi.advanceTimersByTimeAsync(30000));
      const ok = modelReads();
      await act(() => vi.advanceTimersByTimeAsync(2000)); // a good read resets the wait
      expect(modelReads()).toBe(ok + 1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("读取失败后不再需要刷新：下次开始刷新（下载 GPU 版本）时第一次读取又失败，重试间隔从正常间隔算起", async () => {
    vi.useFakeTimers();
    try {
      fake.modelList = [RT({ accelerable: true, backend: "auto", device: "auto", detecting: true, accel: { ...ACCEL } })];
      await loadConfig();
      const read = fake.models.bind(fake);
      let failing = true;
      fake.models = async () => {
        if (!failing) return read();
        fake.calls.push("models");
        throw new Error("ENGINE_BUSY");
      };
      const n = modelReads();
      await act(() => vi.advanceTimersByTimeAsync(2000 + 4000 + 8000)); // three failed reads: the next would wait 16s
      expect(modelReads()).toBe(n + 3);
      // the detection ended, and the settings were read again with the model states: nothing left to wait for
      failing = false;
      Object.assign(fake.modelList[0]!, { detecting: false, deviceCode: "PLUGIN_EXITED_EARLY" });
      await act(() => loadConfig());
      expect(getState().models[0]!.runtime).toMatchObject({ detecting: false });
      const stopped = modelReads();
      await act(() => vi.advanceTimersByTimeAsync(60000));
      expect(modelReads()).toBe(stopped);
      // a download starts the polling again, and its first read fails too
      failing = true;
      await act(async () => {
        downloadAccel("astrlink-guard-int8");
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(getState().models[0]!.runtime!.accel!.status).toBe("downloading");
      expect(modelReads()).toBe(stopped + 1);
      // one failure: 700ms doubled once, not the 30s the earlier failures had reached
      await act(() => vi.advanceTimersByTimeAsync(1399));
      expect(modelReads()).toBe(stopped + 1);
      await act(() => vi.advanceTimersByTimeAsync(1));
      expect(modelReads()).toBe(stopped + 2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("GPU 版本校验未通过：重新下载前先丢弃残留；试运行失败时重新准备", async () => {
    fake.modelList = [RT({ accelerable: true, accel: { ...ACCEL, state: "error", code: "MODEL_VERIFY_FAILED" } })];
    await loadConfig();
    expect(getState().models[0]!.runtime!.accel).toMatchObject({ status: "error", error: "integrity" });
    act(() => downloadAccel("astrlink-guard-int8"));
    await waitFor(() => expect(fake.calls).toContain("downloadModel:astrlink-guard-fp16"));
    expect(fake.calls.indexOf("discardModel:astrlink-guard-fp16")).toBeLessThan(fake.calls.indexOf("downloadModel:astrlink-guard-fp16"));
    fake.modelList[0]!.accel = { ...ACCEL, state: "error", code: "MODEL_PREPARE_FAILED", downloaded: ACCEL.bytes };
    await loadConfig();
    act(() => downloadAccel("astrlink-guard-int8"));
    await waitFor(() => expect(fake.calls).toContain("prepareModel:astrlink-guard-fp16"));
  });

  it("只改主题也会写回", async () => {
    act(() => setThemePref("dark"));
    await waitFor(() => expect(fake.calls).toContain("saveSettings"));
    expect(fake.stored.theme).toBe("dark");
    expect(getState().theme).toBe("dark");
  });

  it("设置写回被拒绝：提示原因并恢复为存储的内容", async () => {
    fake.settingsFails = new Error("INVALID_REQUEST");
    act(() => setSetting("tier", "std"));
    await waitFor(() => expect(toastText()).toContain("不符合要求"));
    fake.settingsFails = null;
    await waitFor(() => expect(getState().settings.tier).toBe("comfort"));
    await saveSettingsNow();
  });

  it("测试方案：先写回未保存的改动，再按存储编号交给内核；结果标出来源", async () => {
    selectPreset("builtin-preset-model");
    act(() => copyPreset());
    const copy = getState().presets.find((p) => !p.builtin)!;
    fake.testResult = { rulesOnly: false, model: "astrlink-guard-int8", minConfidence: 0.8, hits: [
      { start: 0, end: 11, category: "phone", source: "rule", rule: "cn_mobile", ruleName: "手机号" },
      { start: 0, end: 11, category: "phone", source: "model", score: 0.93 },
      { start: 12, end: 15, category: "person", source: "model", score: 0.61, low: true, below: true },
    ] };
    const r = await runPresetTest("13812345678 王小明");
    const stored = getState().presets.find((p) => p.id === getState().presetSel)!.stored!.id;
    expect(stored).not.toBe(copy.id);
    expect(fake.calls.indexOf("configCreate:preset")).toBeLessThan(fake.calls.indexOf("testPreset:" + stored));
    expect(fake.tested).toEqual({ preset: stored, text: "13812345678 王小明" });
    expect(r.minConfidence).toBe(0.8);
    expect(r.hits.map((h) => [h.cat, h.rules, h.model?.score, h.model?.below])).toEqual([["phone", ["手机号"], 0.93, false], ["person", [], 0.61, true]]);
    fake.testFails = new Error("MODEL_NOT_INSTALLED");
    await expect(runPresetTest("x")).rejects.toThrow("MODEL_NOT_INSTALLED");
    expect(testErrorText(new Error("MODEL_NOT_INSTALLED"))).toContain("模型尚未就绪");
    expect(testErrorText(new Error("TIMEOUT"))).toContain("超时");
  });

  it("flushConfig 在没有引擎时什么也不做", async () => {
    setService(null);
    await flushConfig();
  });
});
