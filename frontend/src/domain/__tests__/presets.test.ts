import { describe, expect, it } from "vitest";
import { availableRulesets, bumpVersion, confidenceOf, duplicate, findPresetByName, formatConfidence, presetConfidence, presetModelId, presetNeedsModel, ruleSummary } from "../presets";
import { seedModels, seedPlugins, seedPresets, seedRulesets } from "../seed";

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
    expect(c).toMatchObject({ id: "p2", name: "通用办公脱敏 副本", ver: "v1", builtin: false, hist: [] });
    c.cats.name = "off";
    expect(p.cats.name).toBe("mask");
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
