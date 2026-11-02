import { describe, expect, it } from "vitest";
import { availableRulesets, bumpVersion, categorySourceLabel, presetCategorySources, confidenceOf, duplicate, findPresetByName, formatConfidence, presetConfidence, presetCategories, presetModelId, presetNeedsModel, ruleSummary } from "../presets";
import { STANDARD_CATEGORIES, type CategoryInfo } from "../categories";
import { seedModels, seedPlugins, seedPresets, seedRulesets } from "../seed";
import type { Rule } from "../types";

describe("置信度", () => {
  it("默认值与下限来自所绑定的模型", () => {
    expect(confidenceOf(seedModels()[0])).toEqual({ rec: 0.5, min: 0.3 });
    expect(confidenceOf(undefined)).toEqual({ rec: 0.5, min: 0.3 });
    expect(formatConfidence(0.5)).toBe("0.50");
  });

  it("方案没有设置时使用模型推荐值", () => {
    const p = { ...seedPresets()[0]!, conf: null };
    expect(presetConfidence(p, seedModels())).toBe(0.5);
    expect(presetConfidence({ ...p, conf: 0.7 }, seedModels())).toBe(0.7);
  });
});

describe("方案", () => {
  it("完整列出模型类别并合并启用规则，随模型和检测方式更新", () => {
    const p = seedPresets()[0]!;
    const models = seedModels();
    const rules = seedRulesets();
    const cats = presetCategories(p, models, rules);
    expect(cats).toEqual(["person", "phone", "email", "id_number", "address", "payment_card", "date", "account", "ip_address", "url", "secret"]);
    expect(cats).not.toContain("organization"); // no source of this scheme detects organization names
    // the enterprise terms report organization names and “其他”: both are offered, in catalog order
    expect(presetCategories({ ...p, rules: ["cn", "corp"] }, models, rules).slice(-2)).toEqual(["organization", "other"]);
    models.push({ ...models[0]!, id: "pf", categories: ["person", "address", "date", "phone", "email", "account", "url", "secret"] });
    const minimal = [{ ...rules[0]!, rules: rules[0]!.rules.filter((r) => r.cat === "phone") }];
    expect(presetCategories({ ...p, model: "pf" }, models, minimal)).not.toContain("ip_address");
    expect(presetCategories({ ...p, model: "pf" }, models, minimal)).not.toContain("payment_card");
    expect(presetCategories({ ...p, det: "rules" }, models, minimal)).toEqual(["phone"]);
    minimal[0]!.rules.forEach((r) => { r.on = false; });
    expect(presetCategories({ ...p, det: "rules" }, models, minimal)).toEqual([]);
  });

  it("没有固定类别表：规则或模型报告的任何类别（自建类别、模型新声明的类别、公司名）都列出，按目录顺序", () => {
    const p = { ...seedPresets()[0]!, det: "rules" as const, rules: ["all"] };
    const custom: CategoryInfo = { id: "cfg_0123456789abcdef01234567", name: "项目代号", placeholder: "[项目代号]", origin: "custom" };
    const catalog = [...STANDARD_CATEGORIES, custom];
    const rule = (cat: string): Rule => ({ name: cat, kind: "正则", cat, body: "x", on: true });
    const sets = [{ id: "all", name: "all", ver: "v1", rules: [rule(custom.id), rule("organization"), rule("phone")] }];
    expect(presetCategories(p, [], sets, catalog)).toEqual(["phone", "organization", custom.id]);
    const plates = { id: "plates", categories: ["license_plate", "person"] };
    expect(presetCategories({ ...p, det: "model", model: "plates" }, [plates], sets, catalog)).toEqual(["person", "phone", "organization", custom.id, "license_plate"]);
  });

  it("类别的检测来源：模型、规则或两者", () => {
    const p = seedPresets()[0]!;
    const models = [...seedModels(), { ...seedModels()[0]!, id: "pf", categories: ["person", "phone"] }];
    const rules = seedRulesets();
    const astr = presetCategorySources(p, models, rules);
    expect(categorySourceLabel(astr.get("payment_card"))).toBe("模型 + 规则");
    expect(categorySourceLabel(astr.get("person"))).toBe("模型");
    expect(categorySourceLabel(astr.get("id_number"))).toBe("规则");
    expect(astr.has("organization")).toBe(false);
    expect(categorySourceLabel(presetCategorySources({ ...p, model: "pf" }, models, rules).get("payment_card"))).toBe("规则");
    expect(categorySourceLabel(presetCategorySources({ ...p, det: "rules" }, models, rules).get("phone"))).toBe("规则");
  });

  it("仅规则的方案不需要模型", () => {
    const ps = seedPresets();
    expect(presetNeedsModel(ps, "日志清理")).toBe(false);
    expect(presetModelId(ps, "日志清理")).toBeNull();
    expect(presetModelId(ps, "简历脱敏")).toBe("astr");
  });

  it("找不到名字时退回第一个方案", () => {
    const ps = seedPresets();
    expect(findPresetByName(ps, "不存在").id).toBe("office");
  });
});

describe("版本", () => {
  it("每次修改版本号加一，保留最近 5 个版本（含当前）", () => {
    const p = seedPresets()[1]!; // v3, two earlier versions
    for (let i = 0; i < 10; i++) bumpVersion(p);
    expect(p.ver).toBe("v13");
    expect(p.hist).toHaveLength(4);
    expect(p.hist![0]!.ver).toBe("v12");
    expect(p.edited).toBe("刚刚");
  });

  it("记录的是修改前的内容，不含名称", () => {
    const p = seedPresets()[1]!;
    const before = JSON.stringify(p.cats);
    bumpVersion(p);
    p.cats.phone = "off";
    expect(JSON.stringify(p.hist![0]!.data.cats)).toBe(before);
    expect(Object.keys(p.hist![0]!.data).sort()).toEqual(["cats", "conf", "det", "img", "model", "rules"]);
  });

  it("规则集的版本只含规则", () => {
    const r = seedRulesets()[1]!;
    bumpVersion(r);
    expect(Object.keys(r.hist![0]!.data)).toEqual(["rules"]);
  });

  it("复制得到独立副本：新名称、v1、无历史、可编辑", () => {
    const p = seedPresets()[0]!;
    const c = duplicate(p, "p2");
    expect(c).toMatchObject({ id: "p2", name: "办公外发 副本", ver: "v1", builtin: false, hist: [] });
    c.cats.person = "off";
    expect(p.cats.person).toBe("mask");
  });
});

describe("规则集", () => {
  it("插件规则包只在插件启用时可用", () => {
    const plugins = seedPlugins();
    expect(availableRulesets(seedRulesets(), plugins).map((r) => r.id)).toContain("fin");
    plugins.find((p) => p.id === "fin")!.on = false;
    expect(availableRulesets(seedRulesets(), plugins).map((r) => r.id)).not.toContain("fin");
  });

  it("词表显示条数，其他显示内容", () => {
    expect(ruleSummary({ kind: "词表", n: 36, body: "" })).toBe("36 条");
    expect(ruleSummary({ kind: "词表", body: "a\nb\n\n" })).toBe("2 条");
    expect(ruleSummary({ kind: "正则", body: "\\d+" })).toBe("\\d+");
  });
});
