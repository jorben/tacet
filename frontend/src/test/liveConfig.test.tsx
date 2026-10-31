import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { copyPreset, newRuleset, saveRule, selectPreset, selectRuleset, setCategoryTreatment, rollback } from "../actions/presets";
import { installSamplePlugin, togglePlugin } from "../actions/plugins";
import { checkForUpdate, setSetting, setThemePref } from "../actions/settings";
import { exportLive, flushConfig, saveSettingsNow, importLive, loadConfig, openVersions, stopConfigSync } from "../actions/liveConfig";
import { takePaths } from "../actions/live";
import { overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import type { ConfigItem } from "../services/types";
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
const P_MODEL = item("preset", "builtin-preset-model", "办公外发（规则 + 模型）", { ruleset_id: "builtin-cn-office", scheme_id: "builtin-scheme-model", model_profile_id: "builtin-model-astrlink", mode: "guided", rules_only: false, min_confidence: 0.8, memory_bytes: 1073741824, dpi: 200, style: "solid", retention_days: 0 });
const P_RULES = item("preset", "builtin-preset-office", "办公外发（仅规则）", { ruleset_id: "builtin-cn-office", scheme_id: "builtin-scheme-placeholder", mode: "guided", rules_only: true, memory_bytes: 1073741824, dpi: 200, style: "solid", retention_days: 0 });

let fake: FakeEngine;
const writes = () => fake.calls.filter((c) => /^config(Create|Save|Delete|Rollback)/.test(c));
const toastText = () => overlayStore.get().toast?.msg ?? "";
const sleep = (ms: number) => act(async () => { await new Promise((r) => setTimeout(r, ms)); });

beforeEach(async () => {
  fake = new FakeEngine();
  fake.items = [RULES, SCHEME, SCHEME_RO, MODEL, P_MODEL, P_RULES].map((i) => ({ ...i }));
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
    expect(s.presets.map((p) => [p.name, p.det, p.builtin])).toEqual([["办公外发（规则 + 模型）", "model", true], ["办公外发（仅规则）", "rules", true]]);
    expect(s.homePreset).toBe("办公外发（规则 + 模型）");
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
    expect(copy.name).toBe("办公外发（规则 + 模型） 副本");
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
    // the person changes it: now the categories the rule sets detect are brought along
    selectPreset("cfg_p");
    act(() => setCategoryTreatment("email", "mask"));
    await waitFor(() => expect(fake.calls).toContain("configSave:cfg_s"));
    expect(JSON.parse(fake.items.find((i) => i.id === "cfg_s")!.content).category_actions).toMatchObject({ email: "mask", other: "placeholder" });
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
    selectPreset("builtin-preset-office");
    act(() => copyPreset());
    await waitFor(() => expect(getState().presets.find((p) => !p.builtin)?.stored).toBeTruthy());
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

  it("导出写文件（不含模型）；导入读文件，名称相同时先让人改名，不覆盖", async () => {
    await exportLive("preset", "builtin-preset-model");
    expect(fake.saved!.name).toBe("办公外发（规则 + 模型）.json");
    expect(fake.saved!.text).not.toContain("builtin-model-astrlink");
    expect(JSON.parse(fake.saved!.text)).toMatchObject({ kind: "scheme", rulesets: ["办公文档规则"], detection: "model" });
    // the same file comes back: the name is taken
    fake.openText = fake.saved!.text;
    await importLive("preset");
    expect(overlayStore.get().modal).toMatchObject({ type: "importConflict", kind: "preset" });
    expect(getState().presets).toHaveLength(2);
    const { finishImport } = await import("../actions/presets");
    act(() => finishImport("preset", "我的版本"));
    await waitFor(() => expect(getState().presets.find((p) => p.name === "我的版本")?.stored).toBeTruthy());
    expect(getState().presets.find((p) => p.name === "我的版本")).toMatchObject({ det: "model", ver: "v1" });
    // a file that is not ours changes nothing
    fake.openText = "{";
    await importLive("preset");
    expect(toastText()).toContain("不是 Tacet 的配置文件");
    expect(getState().presets).toHaveLength(3);
    fake.openText = "";
    await importLive("ruleset"); // cancelled
    expect(getState().rulesets).toHaveLength(1);
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
    expect(s.settings).toEqual({ redact: "plaid", tier: "std", autoUpd: true, keep: "0", closeAct: "quit" });
    expect(s.themePref).toBe("dark");
    expect(s.theme).toBe("dark");
    await sleep(500);
    expect(fake.calls).not.toContain("saveSettings");
    act(() => { setSetting("keep", "30"); setSetting("tier", "comfort"); setThemePref("light"); });
    await waitFor(() => expect(fake.calls).toContain("saveSettings"));
    expect(fake.calls.filter((c) => c === "saveSettings")).toHaveLength(1);
    expect(fake.stored).toEqual({ closeBehavior: "quit", theme: "light", redactStyle: "plaid", resourceTier: "comfort", retentionDays: 30, autoUpdate: true });
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

  it("flushConfig 在没有引擎时什么也不做", async () => {
    setService(null);
    await flushConfig();
  });
});
