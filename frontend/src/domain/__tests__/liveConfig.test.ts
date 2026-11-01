import { describe, expect, it } from "vitest";
import type { ConfigItem } from "../../services/types";
import {
  exportRuleset, exportScheme, importPackage, modelFromItem, presetContents, presetFromItems, rulesetContent, rulesetFromItem, sameContent,
} from "../liveConfig";

const item = (kind: string, id: string, content: unknown, extra: Partial<ConfigItem> = {}): ConfigItem => ({
  id, kind, name: id, note: "", version: 1, readOnly: false, content: JSON.stringify(content), ...extra,
});

const RULES = {
  rules: [
    { id: "cn_mobile", category: "phone", pattern: "1[3-9][0-9]{9}", boundary: "digit" },
    { id: "bank", category: "payment_card", pattern: "[0-9]{16}", validator: "luhn", boundary: "cjk" },
    { id: "staff", name: "工号", category: "employee", pattern: "HZ[0-9]{6}", context: ["工号"], context_window: 12, suspect: true },
    { id: "codes", name: "项目代号", category: "other", terms: ["北斗", "银河"], fold_case: true },
    { id: "off", name: "停用", category: "email", pattern: "x@y", disabled: true },
  ],
};
const MODEL = item("model_profile", "m", { model_id: "astrlink-guard-int8", manifest_digest: "sha256:aa", backend: "cpu", adapter_floor: 0.5, calibrated: 0.8, label_map: { private_person: "person", phone: "phone", private_address: "address" }, memory_bytes: 1 });
const model = modelFromItem(MODEL)!.model;
const models = [model];

describe("规则集 ↔ 存储内容", () => {
  it("读出再写回，内容不变：校验器、上下文词、cjk 边界、词表、停用都保留", () => {
    const stored = item("ruleset", "rs1", RULES);
    const rs = rulesetFromItem(stored);
    expect(sameContent(rulesetContent(rs), stored.content)).toBe(true);
    expect(rs.rules.map((r) => [r.name, r.kind, r.on])).toEqual([["cn_mobile", "正则", true], ["bank", "校验", true], ["工号", "正则", true], ["项目代号", "词表", true], ["停用", "正则", false]]);
    expect(rs.rules[3]!.body).toBe("北斗\n银河");
  });

  it("屏幕上新增的规则得到不与其他规则重复的编号，已有编号不变", () => {
    const rs = rulesetFromItem(item("ruleset", "rs1", { rules: [{ id: "rs1.1", category: "phone", pattern: "1" }] }));
    rs.rules.push({ name: "新", kind: "词表", cat: "name", body: "甲\n 乙 \n", on: true }, { name: "又新", kind: "正则", cat: "other", body: "Q+", bd: "word", on: true });
    const out = JSON.parse(rulesetContent(rs)).rules;
    expect(out.map((r: { id: string }) => r.id)).toEqual(["rs1.1", "rs1.2", "rs1.3"]);
    expect(out[1]).toMatchObject({ name: "新", category: "person", terms: ["甲", "乙"] });
    expect(out[2]).toMatchObject({ category: "other", pattern: "Q+", boundary: "word" });
  });

  it("没有名称的规则不会因保存而多出名称", () => {
    const stored = item("ruleset", "r", { rules: [{ id: "a", category: "phone", pattern: "1" }] });
    expect(sameContent(rulesetContent(rulesetFromItem(stored)), stored.content)).toBe(true);
  });

  it("sameContent 与键顺序、空白无关，对值敏感", () => {
    expect(sameContent('{"a":1,"b":[1,2]}', ' {"b":[1,2], "a":1} ')).toBe(true);
    expect(sameContent('{"a":1}', '{"a":2}')).toBe(false);
    expect(sameContent("{", "{")).toBe(false);
  });
});

describe("方案 ↔ 存储对象", () => {
  const rs = [rulesetFromItem(item("ruleset", "cn", RULES, { readOnly: true })), rulesetFromItem(item("ruleset", "co", { rules: [{ id: "x", name: "公司", category: "organization", terms: ["甲公司"] }] }))];
  const scheme = item("scheme", "sc", { category_actions: { phone: "placeholder", payment_card: "mask", person: "placeholder", employee: "delete", other: "placeholder" }, image_default: "review" });
  const preset = item("preset", "pr", { ruleset_id: "cn", extra_ruleset_ids: ["co"], scheme_id: "sc", model_profile_id: "m", mode: "guided", rules_only: false, min_confidence: 0.7, memory_bytes: 5, dpi: 150, style: "mosaic", retention_days: 3 });

  it("两个存储对象显示为一个方案：处理方式、规则集、置信度、图像默认值", () => {
    const p = presetFromItems(preset, scheme, rs, models);
    expect(p).toMatchObject({ det: "model", conf: 0.7, rules: ["cn", "co"], img: "keep", ver: "v1", stored: { id: "pr", schemeId: "sc" } });
    expect(p.cats).toMatchObject({ phone: "ph", card: "mask", name: "ph", email: "off" });
    expect(p.cats).not.toHaveProperty("org");
  });

  it("没有模型时按仅规则显示，模型才能找的类别为“不适用”", () => {
    const p = presetFromItems({ ...preset, content: JSON.stringify({ ...JSON.parse(preset.content), rules_only: true }) }, scheme, rs, []);
    expect(p).toMatchObject({ det: "rules", conf: null });
    expect(p.cats.name).toBe("ph"); // stored as handled; the screen shows what is stored
  });

  it("写回：保留屏幕没有开关的设置，类别按规则/模型的覆盖取舍", () => {
    const p = presetFromItems(preset, scheme, rs, models);
    const w = presetContents(p, rs, models, { scheme, preset });
    expect(sameContent(w.scheme, scheme.content)).toBe(true);
    expect(sameContent(w.preset("sc"), preset.content)).toBe(true);
    expect(w.dropped).toEqual([]);
  });

  it("没有任何规则或模型检测的类别不写入，并报告", () => {
    const p = presetFromItems(preset, scheme, rs, models);
    p.cats.addr = "mask";
    const w = presetContents({ ...p, rules: ["cn"], det: "rules" }, rs, [], { scheme, preset });
    expect(w.dropped.sort()).toEqual(["addr", "name"]);
    const s = JSON.parse(w.scheme);
    expect(Object.keys(s.category_actions).sort()).toEqual(["employee", "other", "payment_card", "phone"]);
    expect(JSON.parse(w.preset("sc"))).toMatchObject({ rules_only: true, ruleset_id: "cn" });
    expect(JSON.parse(w.preset("sc")).model_profile_id).toBeUndefined();
  });

  it("模型新增类别可调整、关闭并在重载及导入导出后保留", () => {
    const extended = [{ ...model, categories: [...model.categories, "account", "ip_address", "url", "secret"] }];
    const p = presetFromItems(preset, scheme, rs, extended);
    expect(p.cats).toMatchObject({ account: "off", ip_address: "off", url: "off", secret: "off" });
    p.cats.url = "ph";
    p.cats.secret = "ph";
    p.cats.account = "mask";
    p.cats.ip_address = "off";
    const w = presetContents(p, rs, extended, { scheme, preset });
    expect(JSON.parse(w.scheme).category_actions).toMatchObject({ account: "mask", url: "placeholder", secret: "placeholder" });
    expect(JSON.parse(w.scheme).category_actions).not.toHaveProperty("ip_address");
    const reloaded = presetFromItems(preset, { ...scheme, content: w.scheme }, rs, extended);
    expect(reloaded.cats).toMatchObject({ account: "mask", ip_address: "off", url: "ph", secret: "ph" });
    const imported = importPackage(exportScheme(p, rs, extended), "preset", rs, extended);
    if (!("preset" in imported)) throw new Error(JSON.stringify(imported));
    expect(imported.preset.cats).toMatchObject({ account: "mask", ip_address: "off", url: "ph", secret: "ph" });
    const plugin = modelFromItem({ ...MODEL, content: JSON.stringify({ ...JSON.parse(MODEL.content), label_map: { a: "account", b: "account", s: "secret" } }) })!.plugin;
    expect(plugin.categories).toEqual(["account", "secret"]);
  });

  it("置信度不低于模型的适配器下限；新方案的资源档位取默认", () => {
    const p = presetFromItems(preset, scheme, rs, models);
    p.conf = 0.1;
    expect(JSON.parse(presetContents(p, rs, models).preset("s"))).toMatchObject({ min_confidence: 0.5, memory_bytes: 1 << 30, dpi: 200, mode: "guided" });
  });

  it("屏幕上改的类别处理方式反映到方案内容", () => {
    const p = presetFromItems(preset, scheme, rs, models);
    p.cats.phone = "mask";
    p.cats.card = "off";
    const a = JSON.parse(presetContents(p, rs, models, { scheme, preset }).scheme).category_actions;
    expect(a).toMatchObject({ phone: "mask", person: "placeholder" });
    expect(a.payment_card).toBeUndefined();
  });
});

describe("交换文件", () => {
  const rs = [rulesetFromItem(item("ruleset", "cn", RULES)), rulesetFromItem(item("ruleset", "co", { rules: [{ id: "x", name: "公司", category: "organization", terms: ["甲公司"] }] }, { name: "企业词条" }))];
  rs[0]!.name = "中国规则";
  const scheme = item("scheme", "sc", { category_actions: { phone: "placeholder", person: "mask" }, image_default: "remove" });
  const preset = item("preset", "pr", { ruleset_id: "cn", extra_ruleset_ids: ["co"], scheme_id: "sc", model_profile_id: "m", mode: "guided", rules_only: false, min_confidence: 0.7, memory_bytes: 5, dpi: 150, style: "solid", retention_days: 0 }, { name: "我的方案" });

  it("规则集导出再导入得到相同的规则，文件里没有编号、版本或历史", () => {
    const text = exportRuleset(rs[1]!);
    expect(text).not.toMatch(/"id"\s*:\s*"co"/);
    const got = importPackage(text, "ruleset", rs, []);
    expect(got).toMatchObject({ kind: "ruleset", name: "企业词条" });
    if (!("ruleset" in got)) throw new Error();
    expect(got.ruleset.rules[0]).toMatchObject({ name: "公司", kind: "词表", body: "甲公司" });
    expect(got.ruleset.stored).toBeUndefined();
  });

  it("方案导出按名称引用规则集、不含模型；导入后重新接上同名规则集和当前模型", () => {
    const p = presetFromItems(preset, scheme, rs, models);
    const text = exportScheme(p, rs);
    expect(text).not.toContain("m\"");
    expect(text).not.toContain("model_profile");
    const parsed = JSON.parse(text);
    expect(parsed.rulesets).toEqual(["中国规则", "企业词条"]);
    const got = importPackage(text, "preset", rs, models);
    if (!("preset" in got)) throw new Error(JSON.stringify(got));
    expect(got.preset).toMatchObject({ name: "我的方案", det: "model", conf: 0.7, rules: ["cn", "co"], ver: "v1" });
    expect(got.preset.cats.phone).toBe("ph");
    expect(got.preset.stored).toBeUndefined();
    // no model here: the same file becomes a rules-only scheme
    const rulesOnly = importPackage(text, "preset", rs, []);
    expect("preset" in rulesOnly && rulesOnly.preset.det).toBe("rules");
  });

  it("不是配置文件、类型不符、缺规则集、内容不合要求都被拒绝", () => {
    expect(importPackage("not json", "ruleset", rs, [])).toEqual({ error: "format" });
    expect(importPackage('{"tacet_config":2,"name":"x"}', "ruleset", rs, [])).toEqual({ error: "format" });
    expect(importPackage('{"tacet_config":1,"name":" "}', "ruleset", rs, [])).toEqual({ error: "format" });
    expect(importPackage(exportRuleset(rs[1]!), "preset", rs, [])).toEqual({ error: "kind" });
    expect(importPackage(exportScheme(presetFromItems(preset, scheme, rs, models), rs), "ruleset", rs, [])).toEqual({ error: "kind" });
    expect(importPackage(exportScheme(presetFromItems(preset, scheme, rs, models), rs), "preset", [rs[0]!], models)).toEqual({ error: "rulesets", detail: "企业词条" });
    expect(importPackage('{"tacet_config":1,"kind":"ruleset","name":"n","rules":[{"id":"a","category":"phone"}]}', "ruleset", rs, [])).toMatchObject({ error: "content" });
  });
});
