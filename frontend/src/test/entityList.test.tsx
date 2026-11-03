import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { RulesEditor } from "../features/presets/RulesEditor";
import { ModelMappingEditor } from "../features/presets/ModelMappingEditor";
import { OverlayHost } from "../features/shell/OverlayHost";
import { getState, store } from "../store";
import { selectRuleset, setCategoryRulesOn } from "../actions/presets";
import { setCategoryLabels } from "../actions/modelMapping";
import { flushConfig, loadConfig, stopConfigSync } from "../actions/liveConfig";
import { overlayStore } from "../overlay/store";
import { effectiveLabels } from "../domain/liveConfig";
import * as liveConfigDomain from "../domain/liveConfig";
import { rulesetChangeLoss } from "../domain/presets";
import type { Rule } from "../domain/types";
import { setService } from "../services";
import { mockService } from "../services/mock";
import type { ConfigItem } from "../services/types";
import { FakeEngine } from "./liveFake";
import { $, $$, button, click, key, pick, press, type as typeInto } from "./helpers";
import office from "../../../resources/rules/cn-office-v1.json";
import privacy from "../../../resources/models/privacy-filter-fp16.json";

const rule = (name: string, cat = "phone", on = true): Rule => ({ name, cat, on, kind: "正则", body: name, bd: "digit" });
const selected = () => getState().rulesets.find((r) => r.id === getState().rulesetSel)!;
const group = (cat: string) => $(`.rule-group[data-category="${cat}"]`)!;
const mapping = (cat: string) => $(`.mapping-row[data-category="${cat}"]`)!;
const ruleRow = (name: string) => $$(".rule-row").find((r) => r.querySelector(".rule-name")?.textContent === name)!;
function demo(rules: Rule[] = [rule("A"), rule("B", "email"), rule("C")]) {
  store.update((d) => { d.rulesets.push({ id: "test", name: "测试集", ver: "v1", hist: [], rules }); d.rulesetSel = "test"; });
}
function mount(kind: "rules" | "model" = "rules") { render(<><div>{kind === "rules" ? <RulesEditor /> : <ModelMappingEditor />}</div><OverlayHost /></>); }
const choose = (cat: string) => click(button(categoryNameFor(cat) + " 关联的标签", mapping(cat)));
const categoryNameFor = (cat: string) => getState().categories.find((c) => c.id === cat)?.name ?? cat;
const finish = async () => { await act(async () => { press("完成", "#layer .pop"); }); };
const confirm = async () => { await act(async () => { click($(".loss-modal .btn-d")!); }); };
afterEach(() => { cleanup(); stopConfigSync(); setService(null); });

describe("实体清单 · 规则页", () => {
  it("目录顺序分组，无空组；目录外原始 id 排最后并标未登记实体；单行和组内保留边界 Tag 与摘要", () => {
    demo([rule("外部", "outside"), rule("B", "email"), rule("A"), rule("C")]); mount();
    expect($$(".rule-group").map((e) => e.dataset.category)).toEqual(["phone", "email", "outside"]);
    expect(group("outside")).toHaveTextContent("outside未登记实体");
    expect(group("email").querySelector(".rule-group-head")).toBeNull();
    for (const n of ["A", "B", "C"]) { expect(ruleRow(n)).toHaveTextContent("边界 · 数字"); expect(ruleRow(n).querySelector(".rs")).toHaveTextContent(n); }
  });
  it("词表摘要保留词条数", () => {
    demo([{ ...rule("词条", "other", false), kind: "词表", body: "甲\n乙", bd: "" }]); mount();
    expect(ruleRow("词条").querySelector(".rs")).toHaveTextContent("2 条");
  });
  it.each([false, true])("编辑已有规则保留启停状态 %s，开关、组头计数和总开关状态不变", (on) => {
    demo([rule("A"), rule("C", "phone", on)]); mount();
    const beforeCount = group("phone").querySelector(".muted.sm")!.textContent;
    const beforeState = within(group("phone")).getByRole("checkbox").getAttribute("aria-checked");
    click(button("编辑", ruleRow("C"))); typeInto("#rdName", "C edited"); typeInto("#rdBody", "C[0-9]+"); press("保存", ".modal");
    expect(selected().rules[1]!.on).toBe(on);
    expect(button("启用 C edited", ruleRow("C edited"))).toHaveAttribute("aria-checked", String(on));
    expect(group("phone").querySelector(".muted.sm")!.textContent).toBe(beforeCount);
    expect(within(group("phone")).getByRole("checkbox")).toHaveAttribute("aria-checked", beforeState);
  });
  it("新建规则保存后默认启用", () => {
    demo([]); mount(); press("添加规则"); typeInto("#rdName", "新规则"); typeInto("#rdBody", "甲\n乙"); press("保存", ".modal");
    expect(selected().rules[0]!.on).toBe(true); expect(button("启用 新规则")).toHaveAttribute("aria-checked", "true");
  });
  it("多条规则默认展开；收起展开不保存；切换规则集再返回恢复默认展开", () => {
    demo(); mount(); const before = selected();
    press("收起 电话"); expect(ruleRow("C")).toBeUndefined(); expect(selected()).toBe(before);
    press("展开 电话"); expect(ruleRow("C")).toBeTruthy();
    press("收起 电话"); act(() => selectRuleset("cn")); act(() => selectRuleset("test"));
    expect(ruleRow("C")).toBeTruthy(); expect(selected()).toBe(before);
  });
  it("5 条规则 2 条启用为 mixed，点击部分和关闭态全部启用；演示 ver 只 +1、hist 只 +1 条", () => {
    demo(Array.from({ length: 5 }, (_, i) => rule("R" + i, "phone", i < 2))); mount();
    const box = () => within(group("phone")).getByRole("checkbox");
    expect(box()).toHaveAttribute("aria-checked", "mixed"); expect(group("phone")).toHaveTextContent("2/5 已启用");
    expect(box().parentElement).toHaveAttribute("data-tip", "只影响本规则集内的规则；实体要不要处理，请在方案页设置");
    click(box()); expect(selected().ver).toBe("v2"); expect(selected().hist).toHaveLength(1);
    expect(selected().rules.every((r) => r.on)).toBe(true); expect(box()).toHaveAttribute("aria-checked", "true");
    click(box()); expect(selected().rules).toHaveLength(5); expect(selected().rules.every((r) => !r.on)).toBe(true);
    expect(box()).toHaveAttribute("aria-checked", "false"); expect($(".loss-modal")).toBeNull(); expect($$(".source-warning")).toHaveLength(0);
    click(box()); expect(selected().rules.every((r) => r.on)).toBe(true);
  });
  it.each(["编辑", "删除", "启停"])("交错 [phone A,email B,phone C] 组内第 2 条使用原下标 2：%s 命中 C", (operation) => {
    demo(); mount();
    expect($$(".rule-name").map((e) => e.textContent)).toEqual(["A", "C", "B"]);
    if (operation === "启停") {
      click(button("启用 C", ruleRow("C")));
      expect(selected().rules.map((r) => r.on)).toEqual([true, true, false]);
      expect(group("phone")).toHaveTextContent("2 条规则 · 1 条启用");
      expect(within(group("phone")).getByRole("checkbox")).toHaveAttribute("aria-checked", "mixed");
    } else {
      click(button("编辑", ruleRow("C")));
      expect($("#rdName")).toHaveValue("C");
      if (operation === "编辑") { typeInto("#rdName", "C edited"); press("保存", ".modal"); expect(selected().rules.map((r) => r.name)).toEqual(["A", "B", "C edited"]); }
      else { press("删除", ".modal"); expect(selected().rules.map((r) => r.name)).toEqual(["A", "B"]); }
    }
  });
  it("每组（含单行）添加规则默认本实体，全局默认 other；编辑原实体；改实体后立即重算单行和空组", () => {
    demo(); mount();
    for (const cat of ["phone", "email"]) {
      click(button("在此实体下添加规则", group(cat)));
      expect($('[aria-pressed=true]', $(".typegrid")!)!.textContent).toBe(categoryNameFor(cat));
      press("取消", ".modal");
    }
    press("添加规则"); expect($('[aria-pressed=true]', $(".typegrid")!)!.textContent).toBe("其他"); press("取消", ".modal");
    click(button("编辑", ruleRow("C"))); expect($('[aria-pressed=true]', $(".typegrid")!)!.textContent).toBe("电话");
    press("邮箱", ".typegrid"); press("保存", ".modal");
    expect(group("phone").querySelector(".rule-group-head")).toBeNull(); expect(group("email").querySelectorAll(".rule-row")).toHaveLength(2);
    click(button("编辑", ruleRow("A"))); press("邮箱", ".typegrid"); press("保存", ".modal");
    expect(group("phone")).toBeNull(); expect(group("email").querySelectorAll(".rule-row")).toHaveLength(3);
    click(button("在此实体下添加规则", group("email"))); typeInto("#rdName", "D"); typeInto("#rdBody", "D"); press("保存", ".modal");
    expect(group("email").querySelectorAll(".rule-row")).toHaveLength(4);
  });
  it.each(["builtin", "plugin"])("%s 只读：总开关与单条置灰，没有编辑和添加入口", (kind) => {
    demo(); store.update((d) => { if (kind === "builtin") d.rulesets.at(-1)!.builtin = true; else d.rulesets.at(-1)!.plugin = "fin"; }); mount();
    for (const e of $$('.preset-detail [role=checkbox],.preset-detail [role=switch]')) expect(e).toBeDisabled();
    expect($$('.preset-detail button[aria-label=编辑]')).toHaveLength(0); expect(screen.queryByText("在此实体下添加规则")).toBeNull(); expect(screen.queryByText("添加规则")).toBeNull();
  });
  it.each(["总开关", "单规则"])("仅规则方案 X 失去来源：%s 先确认列出 X，取消不改，确认后停用并显示提醒", async (kind) => {
    demo(kind === "总开关" ? [rule("A"), rule("C")] : [rule("C")]);
    store.update((d) => { d.presets.push({ ...d.presets[0]!, id: "X", name: "X", builtin: false, det: "rules", rules: ["test"], cats: { phone: "ph" } }); }); mount();
    const control = () => kind === "总开关" ? within(group("phone")).getByRole("checkbox") : button("启用 C");
    const before = selected(); click(control());
    expect(overlayStore.get().modal?.type).toBe("detectionLoss");
    expect($(".loss-modal")).toHaveTextContent("方案『X』将无法开始任务"); expect($(".loss-modal")).toHaveTextContent("方案『X』将没有任何启用的规则，开始任务会失败");
    press("取消", ".loss-modal"); expect(selected()).toBe(before);
    click(control()); await confirm(); expect(selected().rules.every((r) => !r.on)).toBe(true);
    expect(group("phone").querySelector("[role=note]")).toHaveTextContent("方案『X』将无法开始任务：该实体没有检测来源");
  });
  it.each(["改实体", "删除"])("%s 也确认，取消返回草稿且不改规则", async (kind) => {
    demo([rule("C")]); store.update((d) => { d.presets.push({ ...d.presets[0]!, id: "X", name: "X", rules: ["test"], det: "rules", cats: { phone: "mask" } }); }); mount();
    click(button("编辑", ruleRow("C"))); typeInto("#rdName", "草稿");
    if (kind === "改实体") { press("邮箱", ".typegrid"); press("保存", ".modal"); } else press("删除", ".modal");
    expect($(".loss-modal")).toHaveTextContent("X"); press("取消", ".loss-modal"); expect($("#rdName")).toHaveValue("草稿"); expect(selected().rules[0]!.cat).toBe("phone");
    press(kind === "改实体" ? "保存" : "删除", ".modal"); await confirm();
    expect(selected().rules.filter((r) => r.cat === "phone")).toHaveLength(0);
  });
  it("额外规则集失源按每个方案分别计算：仅规则忽略模型，其他规则集覆盖则不提醒", () => {
    demo([rule("A")]);
    const s = getState();
    const primary = { id: "primary", name: "主规则集", ver: "v1", rules: [rule("邮箱", "email")] };
    const backup = { id: "backup", name: "补充规则集", ver: "v1", rules: [rule("备用电话")] };
    const sources = { rulesets: [primary, selected(), backup], models: s.models };
    const changed = { ...selected(), rules: [rule("A", "phone", false)] };
    const x = { ...s.presets[0]!, id: "X", name: "X", det: "rules" as const, rules: ["primary", "test"], cats: { phone: "ph" as const } };
    const y = { ...x, id: "Y", name: "Y", rules: ["primary", "test", "backup"] };
    const z = { ...x, id: "Z", name: "Z", det: "model" as const };
    const loss = rulesetChangeLoss([x, y, z], sources, changed);
    expect(loss.categories.map((r) => [r.preset.name, r.categories])).toEqual([["X", ["phone"]]]);
    expect(loss.noRules).toEqual([]);
  });
  it("失源纯函数按每个方案合并其他规则集、模型和处理方式；额外引用也算，未引用不算", () => {
    demo([rule("A")]); const s = getState(), rs = selected();
    const base = { ...s.presets[0]!, id: "X", name: "X", cats: { phone: "mask" as const }, rules: ["cn", "test"] };
    const sources = { rulesets: s.rulesets, models: s.models };
    const changed = { ...rs, rules: [rule("A", "phone", false)] };
    expect(rulesetChangeLoss([base], sources, changed)).toEqual({ categories: [], noRules: [] });
    const p = { ...base, rules: ["test"], det: "rules" as const };
    expect(rulesetChangeLoss([p], sources, changed).categories[0]!.categories).toEqual(["phone"]);
    expect(rulesetChangeLoss([{ ...p, cats: { phone: "off" } }], sources, changed).categories).toHaveLength(0);
    expect(rulesetChangeLoss([{ ...p, rules: ["cn"] }], sources, changed)).toEqual({ categories: [], noRules: [] });
    expect(rulesetChangeLoss([{ ...p, det: "model" }], sources, changed).categories).toHaveLength(0);
    expect(rulesetChangeLoss([{ ...p, det: "model" }], sources, changed).noRules).toHaveLength(1);
  });
});

const item = (kind: string, id: string, name: string, content: unknown, readOnly = true): ConfigItem => ({ kind, id, name, content: JSON.stringify(content), readOnly, version: 1, note: "" });
async function live() {
  const fake = new FakeEngine();
  fake.items = [item("ruleset", "builtin-cn-office", "办公文档规则", office),
    item("ruleset", "custom", "自建集", { rules: office.rules.filter((r) => r.category === "phone") }, false),
    item("model_profile", "profile", "Privacy Filter", { model_id: "privacy-filter-fp16", adapter_floor: privacy.adapter_floor, calibrated: privacy.default_min_score, memory_bytes: 1, label_map: privacy.label_map }),
    item("scheme", "scheme", "X", { category_actions: { person: "mask" }, image_default: "remove" }),
    item("preset", "preset", "X", { ruleset_id: "builtin-cn-office", scheme_id: "scheme", model_profile_id: "profile", rules_only: false })];
  setService({ ...mockService, engine: fake }); await act(async () => { await loadConfig(); }); return fake;
}

describe("实体清单 · live 资源夹具 + FakeEngine", () => {
  it("办公文档规则：secret 5，phone/payment_card/date 各 2；其余单行，目录顺序", async () => {
    await live(); mount();
    expect(group("secret")).toHaveTextContent("5 条规则 · 5 条启用");
    for (const c of ["phone", "payment_card", "date"]) expect(group(c)).toHaveTextContent("2 条规则 · 2 条启用");
    for (const c of ["email", "id_number", "ip_address", "url", "account"]) expect(group(c).querySelector(".rule-group-head")).toBeNull();
    expect($$(".rule-group").map((r) => r.dataset.category)).toEqual(["phone", "email", "id_number", "payment_card", "date", "account", "ip_address", "url", "secret"]);
  });
  it("live 批量停用只 configSave 一次，expected version 正确，disabled 全 true 且规则未删", async () => {
    const fake = await live(); act(() => selectRuleset("custom")); mount();
    const spy = vi.spyOn(fake, "configSave"); click(within(group("phone")).getByRole("checkbox")); await act(async () => { await flushConfig(); });
    expect(spy).toHaveBeenCalledTimes(1); expect(spy.mock.calls[0]!.slice(0, 2)).toEqual(["custom", 1]);
    const out = JSON.parse(fake.items.find((i) => i.id === "custom")!.content).rules;
    expect(out).toHaveLength(2); expect(out.every((r: { disabled: boolean }) => r.disabled)).toBe(true);
  });
  it("重新载入同一规则集也恢复默认展开", async () => {
    await live(); mount(); press("收起 电话"); expect(ruleRow("座机")).toBeUndefined();
    await act(async () => { await loadConfig(); }); expect(ruleRow("座机")).toBeTruthy();
  });
  it("批量启用保存失败 toast 并恢复服务端值，保持当前规则集", async () => {
    const fake = await live(); act(() => selectRuleset("custom")); act(() => setCategoryRulesOn("phone", false)); await act(async () => { await flushConfig(); }); mount();
    fake.configFails = new Error("RULE_ID_DUPLICATE"); click(within(group("phone")).getByRole("checkbox")); await act(async () => { await flushConfig(); });
    expect(overlayStore.get().toast?.msg).toBe("保存未完成（RULE_ID_DUPLICATE）");
    expect(getState().rulesetSel).toBe("custom"); expect(selected().rules.every((r) => !r.on)).toBe(true);
    expect(within(group("phone")).getByRole("checkbox")).toHaveAttribute("aria-checked", "false");
  });
  it("没有声明标签也列出目录全部实体且禁用标签选择", () => {
    store.update((d) => { d.models[0]!.labels = {}; d.models[0]!.categories = []; }); mount("model");
    expect($$(".mapping-row")).toHaveLength(getState().categories.length);
    expect($$(".mapping-row .mapping-labels .muted")).toHaveLength(getState().categories.length);
    expect($$(".mapping-row button").every((e) => e.hasAttribute("disabled"))).toBe(true);
    expect(screen.getByText("这个模型没有声明标签")).toBeTruthy();
  });
  it("privacy-filter 目录全集含用户类别；未设置为灰色，person 标签块，目录外兜底标签不消失", async () => {
    const fake = await live(); fake.categoryList.push({ id: "user", name: "用户实体", placeholder: "[用户]", origin: "custom", manual: true }); await loadConfig(); mount("model");
    expect($$(".mapping-row").map((r) => r.dataset.category)).toEqual(getState().categories.map((c) => c.id));
    for (const c of ["payment_card", "ip_address", "id_number", "organization", "other", "user"]) expect(mapping(c).querySelector(".mapping-labels .muted")).toHaveTextContent("未设置");
    expect(mapping("person").querySelector(".tag.mono")).toHaveTextContent("private_person");
    act(() => store.update((d) => { d.models[0]!.mapping = { map: { ...privacy.label_map, private_person: "unknown" } }; }));
    expect($$(".mapping-row").at(-1)).toBe(mapping("unknown")); expect(mapping("unknown")).toHaveTextContent("unknown未登记实体"); expect(mapping("unknown")).toHaveTextContent("private_person");
  });
  it("取消模型最后标签先确认 X，取消不改，确认后未关联可再分配；恢复默认删除映射和已修改；mapping-warn 保留", async () => {
    const fake = await live(); mount("model"); choose("person"); pick("private_person"); await finish();
    expect(overlayStore.get().modal?.type).toBe("detectionLoss");
    expect($(".loss-modal")).toHaveTextContent("方案『X』"); press("取消", ".loss-modal"); expect(getState().models[0]!.mapping).toBeUndefined();
    choose("person"); pick("private_person"); await finish(); await confirm();
    expect(effectiveLabels(getState().models[0]!)).not.toHaveProperty("private_person"); expect($(".unmapped-labels")).toHaveTextContent("private_person");
    click(button("private_person", $(".unmapped-labels")!)); await act(async () => { pick("公司名"); });
    expect(effectiveLabels(getState().models[0]!).private_person).toBe("organization"); expect($(".mapping-warn")).toHaveTextContent("公司名");
    expect(mapping("person").querySelector("[data-tip]")).toHaveAttribute("data-tip", "默认值：private_person");
    await act(async () => { press("恢复默认"); });
    expect(fake.items.filter((i) => i.kind === "model_mapping")).toHaveLength(0); expect($$(".mapping-row [data-tip]")).toHaveLength(0);
  });
  it("把模型最后标签从 A 移到 B 也先确认；确认后提示已从 A 移到 B", async () => {
    await live(); mount("model"); choose("organization"); pick("private_person"); await finish(); expect($(".loss-modal")).toHaveTextContent("X"); await confirm();
    expect(effectiveLabels(getState().models[0]!).private_person).toBe("organization"); expect(overlayStore.get().toast?.msg).toContain("已从 人名 移到 公司名");
  });
  it.each([false, true])("快速连续勾选 3 个标签（已有自定义 %s）：菜单关闭一次写入，无冲突", async (custom) => {
    const fake = await live(); if (custom) await setCategoryLabels("privacy-filter-fp16", "organization", ["private_date"]);
    const create = vi.spyOn(fake, "configCreate"), save = vi.spyOn(fake, "configSave"); mount("model"); choose("organization");
    for (const l of ["private_phone", "private_email", "account_number"]) pick(l);
    expect(create).not.toHaveBeenCalled(); expect(save).not.toHaveBeenCalled(); await finish();
    await waitFor(() => expect(effectiveLabels(getState().models[0]!).account_number).toBe("organization"));
    expect(create.mock.calls.length + save.mock.calls.length).toBe(1);
    const storedMap = JSON.parse(fake.items.find((i) => i.kind === "model_mapping")!.content).label_map;
    for (const l of ["private_phone", "private_email", "account_number"]) {
      expect(effectiveLabels(getState().models[0]!)[l]).toBe("organization");
      expect(storedMap[l]).toBe("organization");
    }
    expect(overlayStore.get().toast?.msg).not.toMatch(/VERSION_CONFLICT|名称已被使用/);
  });
  it.each(["Escape", "outside"])("多选菜单通过 %s 关闭时只提交一次；勾选期间不写入", async (how) => {
    const fake = await live(); const spy = vi.spyOn(fake, "configCreate"); mount("model"); choose("organization"); pick("private_phone");
    expect(spy).not.toHaveBeenCalled();
    await act(async () => { if (how === "Escape") key("Escape"); else click(mapping("email")); });
    expect(spy).toHaveBeenCalledTimes(1); expect(effectiveLabels(getState().models[0]!).private_phone).toBe("organization");
    await act(async () => { key("Escape"); click(mapping("email")); }); expect(spy).toHaveBeenCalledTimes(1);
  });
  it("同模型前一次操作抛错后，已排队的下一次操作仍执行并写入，调用方分别收到各自结果", async () => {
    const fake = await live();
    const error = new Error("unexpected mapping edit failure");
    const edit = vi.spyOn(liveConfigDomain, "withCategoryLabels").mockImplementationOnce(() => { throw error; });
    try {
      const results = await Promise.allSettled([
        setCategoryLabels("privacy-filter-fp16", "organization", ["private_phone"]),
        setCategoryLabels("privacy-filter-fp16", "organization", ["private_email"]),
      ]);
      expect(results[0]).toEqual({ status: "rejected", reason: error });
      expect(results[1].status).toBe("fulfilled");
      expect(fake.calls.filter((c) => c === "configCreate:model_mapping")).toHaveLength(1);
      const storedMap = JSON.parse(fake.items.find((i) => i.kind === "model_mapping")!.content).label_map;
      expect(storedMap.private_email).toBe("organization");
      expect(storedMap.private_phone).toBe("phone");
      expect(effectiveLabels(getState().models[0]!).private_email).toBe("organization");
    } finally {
      edit.mockRestore();
    }
  });
  it.each([false, true])("同模型写入队列含首次 configCreate（已有自定义 %s）：注入写入延迟，连续操作保留 3 标签", async (custom) => {
    const fake = await live(); if (custom) await setCategoryLabels("privacy-filter-fp16", "organization", ["private_date"]);
    // Delay at the actual engine boundary; without the queue all callers read the same stored ref before this resolves.
    const save = fake.configSave.bind(fake), create = fake.configCreate.bind(fake);
    const rejected: string[] = [];
    fake.configSave = async (...args) => { await new Promise((r) => setTimeout(r, 15)); try { return await save(...args); } catch (err) { rejected.push((err as Error).message); throw err; } };
    fake.configCreate = async (...args) => { await new Promise((r) => setTimeout(r, 15)); try { return await create(...args); } catch (err) { rejected.push((err as Error).message); throw err; } };
    await Promise.all([setCategoryLabels("privacy-filter-fp16", "organization", ["private_phone"]), setCategoryLabels("privacy-filter-fp16", "organization", ["private_phone", "private_email"]), setCategoryLabels("privacy-filter-fp16", "organization", ["private_phone", "private_email", "account_number"])]);
    expect(rejected).toEqual([]);
    expect(overlayStore.get().toast?.msg).not.toMatch(/VERSION_CONFLICT|名称已被使用/);
    const out = JSON.parse(fake.items.find((i) => i.kind === "model_mapping")!.content).label_map;
    expect(Object.keys(out).filter((l) => out[l] === "organization")).toEqual(expect.arrayContaining(["private_phone", "private_email", "account_number"]));
  });
});
