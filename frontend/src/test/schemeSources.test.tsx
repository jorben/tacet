import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { confirmDeleteCategory, createCategory, deleteCategory, removeCategory } from "../actions/categories";
import { flushConfig, loadConfig, stopConfigSync } from "../actions/liveConfig";
import { setLabelCategory } from "../actions/modelMapping";
import { copyPreset, deleteRule, newRuleset, openPresetRulesMenu, saveRule, selectPreset, selectRuleset, setCategoryTreatment } from "../actions/presets";
import { effectiveLabels } from "../domain/liveConfig";
import { OverlayHost } from "../features/shell/OverlayHost";
import { overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import type { ConfigItem } from "../services/types";
import { getState, store } from "../store";
import { $, $$ } from "./helpers";
import { FakeEngine } from "./liveFake";

const item = (kind: string, id: string, name: string, content: unknown, readOnly = true): ConfigItem => ({ id, kind, name, note: "", version: 1, readOnly, content: JSON.stringify(content) });
const RULES = item("ruleset", "builtin-cn-office", "办公文档规则", { rules: [{ id: "cn_mobile", category: "phone", pattern: "1[3-9][0-9]{9}", boundary: "digit" }] });
const SCHEME = item("scheme", "builtin-scheme-model", "占位符方案", { category_actions: { phone: "placeholder", person: "placeholder" }, image_default: "remove" });
const MODEL = item("model_profile", "builtin-model-astrlink", "AstrLink Guard", { model_id: "astrlink-guard-int8", manifest_digest: "sha256:aa", backend: "cpu", adapter_floor: 0.5, calibrated: 0.8, label_map: { private_person: "person", private_address: "address", phone: "phone" }, memory_bytes: 1 });
const PRESET = item("preset", "builtin-preset-model", "办公外发", { ruleset_id: "builtin-cn-office", scheme_id: "builtin-scheme-model", model_profile_id: "builtin-model-astrlink", mode: "guided", rules_only: false, min_confidence: 0.8, memory_bytes: 1, dpi: 200, style: "solid", retention_days: 0 });

let fake: FakeEngine;
const toastText = () => overlayStore.get().toast?.msg ?? "";
const model = () => getState().models[0]!;

beforeEach(async () => {
  fake = new FakeEngine();
  fake.items = [RULES, SCHEME, MODEL, PRESET].map((i) => ({ ...i }));
  setService({ ...mockService, engine: fake });
  await loadConfig();
});
afterEach(() => { cleanup(); stopConfigSync(); setService(null); act(() => overlayStore.replace({ pop: null, modal: null, drawer: null, toast: null })); });

describe("模型映射至少关联一个标签", () => {
  it("取消最后一个关联的标签被拒绝，不写入", async () => {
    await act(async () => { await setLabelCategory(model().id, "private_address", null); });
    await act(async () => { await flushConfig(); });
    await act(async () => { await setLabelCategory(model().id, "phone", null); });
    await waitFor(() => expect(Object.keys(effectiveLabels(model()))).toEqual(["private_person"]));
    const before = fake.calls.length;
    await act(async () => { await setLabelCategory(model().id, "private_person", null); });
    expect(overlayStore.get().modal).toBeNull();
    expect(toastText()).toBe("至少保留一个标签关联到类别");
    expect(fake.calls.slice(before).filter((c) => c.startsWith("config"))).toEqual([]);
    expect(Object.keys(effectiveLabels(model()))).toEqual(["private_person"]);
  });
});

describe("方案的规则组可多选，只列出有规则的规则组", () => {
  it("尚无规则的新规则组不在选择菜单中，加入规则后出现", () => {
    selectPreset("builtin-preset-model");
    act(() => { copyPreset(); newRuleset(); });
    const anchor = document.createElement("button");
    document.body.append(anchor);
    render(<OverlayHost />);
    act(() => openPresetRulesMenu(anchor));
    expect($$("#layer .pop .mi").map((e) => e.textContent).join("|")).not.toContain("新规则组");
    act(() => overlayStore.replace({ ...overlayStore.get(), pop: null }));
    act(() => { saveRule(null, { name: "工号", kind: "正则", cat: "account", bd: "", body: "HZ[0-9]{6}" }); });
    act(() => openPresetRulesMenu(anchor));
    expect($$("#layer .pop .mi").map((e) => e.textContent).join("|")).toContain("新规则组");
  });
});

describe("删除类别", () => {
  it("被规则引用时不能删除；只被模型映射和方案使用时，确认后删除并列出受影响的模型和方案", async () => {
    let id = "";
    await act(async () => { const got = await createCategory("项目代号"); id = "id" in got ? got.id : ""; });
    expect(id).toMatch(/^cfg_/);
    // the model counts addresses as the new category; a copy of the scheme masks it
    await act(async () => { await setLabelCategory(model().id, "private_address", id); });
    await waitFor(() => expect(effectiveLabels(model()).private_address).toBe(id));
    selectPreset("builtin-preset-model");
    act(() => copyPreset());
    act(() => setCategoryTreatment(id, "ph"));
    await act(async () => { await flushConfig(); });
    // a rule reports it: it stays
    act(() => { newRuleset(); saveRule(null, { name: "代号", kind: "词表", cat: id, bd: "", body: "北斗" }); });
    act(() => deleteCategory(id));
    expect(overlayStore.get().modal).toBeNull();
    expect(toastText()).toBe("「项目代号」正被规则组「新规则组」的规则使用，不能删除");
    act(() => { saveRule(0, { name: "代号", kind: "词表", cat: "other", bd: "", body: "北斗" }); });
    // the dialog says what lets go of it
    render(<OverlayHost />);
    act(() => deleteCategory(id));
    expect(overlayStore.get().modal).toMatchObject({ type: "deleteCategory", id });
    const text = $("#layer .modal")!.textContent!;
    expect(text).toContain("类别为全局共用");
    expect(text).toContain("模型『" + model().name + "』中关联到它的标签 private_address 将变为未关联");
    expect(text).toContain("方案『办公外发 副本』中对它的设置将被清除");
    expect(text).toContain("已开始的任务与历史结果保持原样");
    await act(async () => { await confirmDeleteCategory(id); });
    expect(fake.calls).toContain("configDelete:" + id);
    expect(fake.calls.lastIndexOf("categories")).toBeGreaterThan(fake.calls.indexOf("configDelete:" + id));
    expect(getState().categories.some((c) => c.id === id)).toBe(false);
    expect(toastText()).toBe("已删除类别「项目代号」");
  });

  it("模型只关联了这个类别时不能删除", async () => {
    let id = "";
    await act(async () => { const got = await createCategory("全部"); id = "id" in got ? got.id : ""; });
    act(() => store.update((d) => { d.models[0]!.mapping = { map: { private_person: id } }; }));
    act(() => deleteCategory(id));
    expect(overlayStore.get().modal).toBeNull();
    expect(toastText()).toContain("只有关联到「全部」的标签，不能删除");
  });

  it("演示模式：删除后模型的标签变为未关联，方案的设置清除", async () => {
    let id = "";
    await act(async () => { const got = await createCategory("项目代号"); id = "id" in got ? got.id : ""; });
    stopConfigSync();
    setService(null);
    act(() => store.update((d) => {
      d.models[0]!.mapping = { map: { private_person: "person", private_address: id } };
      d.presets[0]!.cats[id] = "mask";
    }));
    await act(async () => { expect(await removeCategory(id)).toBe(true); });
    expect(getState().categories.some((c) => c.id === id)).toBe(false);
    expect(model().mapping!.map).toEqual({ private_person: "person" });
    expect(model().categories).toEqual(["person"]);
    expect(getState().presets[0]!.cats).not.toHaveProperty(id);
  });
});

describe("规则组至少一条规则", () => {
  it("删除最后一条规则被拒绝", () => {
    act(() => { newRuleset(); saveRule(null, { name: "工号", kind: "正则", cat: "account", bd: "", body: "HZ[0-9]{6}" }); });
    selectRuleset(getState().rulesetSel!);
    act(() => deleteRule(0));
    expect(toastText()).toContain("规则组至少保留一条规则");
    expect(getState().rulesets.find((r) => r.id === getState().rulesetSel)!.rules).toHaveLength(1);
  });
});
