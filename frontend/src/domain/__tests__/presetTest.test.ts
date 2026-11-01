import { describe, expect, it } from "vitest";
import { simulateTest, testCounts, testFromEngine, testSegments, testSourceLabel } from "../presetTest";
import { seedModels, seedPresets, seedRulesets } from "../seed";

describe("方案测试：内核结果", () => {
  it("同一片段被模型和规则同时命中时合并为一条，来源为「模型 + 规则」", () => {
    const r = testFromEngine({
      rulesOnly: false, model: "astr", minConfidence: 0.5,
      hits: [
        { start: 3, end: 14, category: "phone", source: "rule", rule: "cn_mobile", ruleName: "手机号" },
        { start: 3, end: 14, category: "phone", source: "model", score: 0.97 },
        { start: 0, end: 2, category: "person", source: "model", score: 0.42, low: true, below: true },
        { start: 20, end: 26, category: "other", source: "dictionary", rule: "corp_terms" },
        { start: 30, end: 36, category: "account", source: "model", score: 0.8 },
      ],
    });
    expect(r.minConfidence).toBe(0.5);
    expect(r.hits.map((h) => [h.start, h.cat, h.label, testSourceLabel(h), h.rules])).toEqual([
      [0, "name", "人名", "模型", []],
      [3, "phone", "电话", "模型 + 规则", ["手机号"]],
      [20, "other", "其他", "规则", ["corp_terms"]],
      [30, "account", "账号", "模型", []],
    ]);
    expect(r.hits[0]!.model).toEqual({ score: 0.42, low: true, below: true });
    expect(r.hits[3]!.color).toBe("account");
    expect(testCounts(r.hits)).toEqual({ total: 4, model: 2, rules: 1, both: 1, below: 1 });
  });

  it("仅规则方案不带最低置信度；空结果", () => {
    expect(testFromEngine({ rulesOnly: true, hits: null })).toEqual({ hits: [], rulesOnly: true });
  });
});

describe("方案测试：文本分段", () => {
  it("重叠的命中归入先出现、更长的那一个", () => {
    const text = "电话 13812345678 结束";
    const r = testFromEngine({ rulesOnly: true, hits: [
      { start: 3, end: 14, category: "phone", source: "rule" },
      { start: 6, end: 10, category: "other", source: "rule" },
    ] });
    expect(testSegments(text, r.hits)).toEqual([
      { text: "电话 ", hit: null }, { text: "13812345678", hit: 0 }, { text: " 结束", hit: null },
    ]);
  });
});

describe("方案测试：浏览器模拟", () => {
  const text = "联系人：王晓明，手机 13812345678，身份证号 11010519491231002X，卡号 6222020200112233446，于 2026年3月15日 签约。";
  const at = (h: { start: number; end: number }) => text.slice(h.start, h.end);

  it("模型 + 规则方案：规则按写法匹配，校验类规则做校验，模型命中带分数", () => {
    const [p] = seedPresets();
    const r = simulateTest(p!, seedRulesets(), seedModels(), text);
    const by = Object.fromEntries(r.hits.map((h) => [at(h), h]));
    expect(testSourceLabel(by["王晓明"]!)).toBe("模型");
    expect(testSourceLabel(by["13812345678"]!)).toBe("模型 + 规则");
    expect(by["13812345678"]!.rules).toEqual(["手机号"]);
    expect(testSourceLabel(by["11010519491231002X"]!)).toBe("规则");
    expect(by["6222020200112233446"]!.cat).toBe("card");
    expect(r.hits.some((h) => at(h) === "11010519491231002")).toBe(false);
    expect(r.minConfidence).toBe(0.5);
  });

  it("仅规则方案只有规则命中；被关闭的类别不出现", () => {
    const p = seedPresets()[2]!; // 日志清理：仅规则，日期关闭
    const r = simulateTest(p, seedRulesets(), seedModels(), text);
    expect(r.rulesOnly).toBe(true);
    expect(r.hits.every((h) => !h.model && h.rules.length)).toBe(true);
    expect(r.hits.some((h) => h.cat === "date")).toBe(false);
    expect(r.hits.map(at)).toContain("13812345678");
  });

  it("高于模型分数的最低置信度：模型命中标为低于最低置信度", () => {
    const p = { ...seedPresets()[0]!, conf: 0.9 };
    const r = simulateTest(p, seedRulesets(), seedModels(), text);
    const name = r.hits.find((h) => at(h) === "王晓明")!;
    expect(name.model?.below).toBe(true);
    expect(testCounts(r.hits).below).toBeGreaterThan(0);
  });
});
