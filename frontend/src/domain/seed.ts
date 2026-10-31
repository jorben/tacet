// What a fresh installation starts with: one model, built-in and sample schemes, rule sets and plugins.
import type { ModelPlugin, PluginInfo, Preset, Ruleset, Settings } from "./types";

export const DEFAULT_PRESET_NAME = "通用办公脱敏";

export const seedModels = (): ModelPlugin[] => [
  { id: "astr", name: "AstrLink Guard", version: "0.1.3", variant: "INT8", languages: "中文 / 中英混排", weightMiB: 36, recommended: true, confRec: 0.5, confMin: 0.3, status: "ready", progress: 100, error: null },
];

export const seedPresets = (): Preset[] => [
  {
    id: "office", name: "通用办公脱敏", ver: "内建", builtin: true, det: "model", model: "astr", conf: 0.5, rules: ["cn"],
    cats: { org: "mask", name: "mask", phone: "mask", email: "mask", id: "mask", addr: "mask", card: "mask", date: "mask" }, img: "remove", edited: "",
  },
  {
    id: "cv", name: "简历脱敏", ver: "v3", det: "model", model: "astr", conf: 0.5, rules: ["cn", "corp"],
    cats: { org: "ph", name: "ph", phone: "mask", email: "mask", id: "mask", addr: "mask", card: "mask", date: "off" }, img: "remove", edited: "2 天前",
    hist: [
      { ver: "v2", when: "上周", data: { det: "model", model: "astr", conf: 0.5, rules: ["cn", "corp"], cats: { org: "ph", name: "ph", phone: "mask", email: "mask", id: "mask", addr: "mask", card: "mask", date: "mask" }, img: "remove" } },
      { ver: "v1", when: "上月", data: { det: "model", model: "astr", conf: 0.5, rules: ["cn"], cats: { org: "mask", name: "mask", phone: "mask", email: "mask", id: "mask", addr: "mask", card: "mask", date: "mask" }, img: "remove" } },
    ],
  },
  {
    id: "log", name: "日志清理", ver: "v1", det: "rules", model: "astr", conf: 0.5, rules: ["cn"],
    cats: { org: "na", name: "na", phone: "mask", email: "ph", id: "mask", addr: "na", card: "mask", date: "off" }, img: "remove", edited: "上月",
  },
];

export const seedRulesets = (): Ruleset[] => [
  {
    id: "cn", name: "中国场景规则集", ver: "2026.10", builtin: true,
    rules: [
      { name: "手机号", kind: "正则", cat: "phone", body: "1[3-9]\\d{9}", bd: "digit", on: true },
      { name: "座机", kind: "正则", cat: "phone", body: "0\\d{2,3}-?\\d{7,8}", bd: "digit", on: true },
      { name: "邮箱", kind: "正则", cat: "email", body: "[\\w.+-]+@[\\w-]+(\\.[\\w-]+)+", on: true },
      { name: "居民身份证", kind: "校验", cat: "id", body: "18 位 · 出生日期 · 校验位", on: true },
      { name: "银行卡", kind: "校验", cat: "card", body: "16–19 位 · Luhn", on: true },
      { name: "日期", kind: "正则", cat: "date", body: "\\d{4}\\s*年\\s*\\d{1,2}\\s*月\\s*\\d{1,2}\\s*日", on: true },
      { name: "API Key / Token", kind: "校验", cat: "other", body: "已知格式 42 种", on: true },
    ],
  },
  {
    id: "corp", name: "企业词条", ver: "v2", edited: "3 天前",
    hist: [{ ver: "v1", when: "上月", data: { rules: [
      { name: "项目代号", kind: "词表", cat: "other", body: "", n: 36, on: true },
      { name: "合作方简称", kind: "词表", cat: "org", body: "", n: 120, on: true },
    ] } }],
    rules: [
      { name: "项目代号", kind: "词表", cat: "other", body: "", n: 36, on: true },
      { name: "合作方简称", kind: "词表", cat: "org", body: "", n: 120, on: true },
      { name: "员工工号", kind: "正则", cat: "other", body: "HZ\\d{6}", on: true },
    ],
  },
  {
    id: "fin", name: "金融行业规则包", ver: "1.2.0", plugin: "fin",
    rules: [
      { name: "证券账户", kind: "正则", cat: "card", body: "[AB]\\d{9}", on: true },
      { name: "SWIFT 代码", kind: "正则", cat: "other", body: "[A-Z]{6}[A-Z0-9]{2}([A-Z0-9]{3})?", on: true },
    ],
  },
];

export const seedPlugins = (): PluginInfo[] => [
  { id: "docx", name: "DOCX", role: "格式", fmt: ".docx", ver: "1.0.0", builtin: true, on: true },
  { id: "pdf", name: "PDF · 文字型", role: "格式", fmt: ".pdf", ver: "1.0.0", builtin: true, on: true },
  { id: "txt", name: "文本 · Markdown · 日志", role: "格式", fmt: ".txt .md .log", ver: "1.0.0", builtin: true, on: true },
  { id: "csv", name: "CSV · JSONL", role: "格式", fmt: ".csv .jsonl", ver: "1.0.0", builtin: true, on: true },
  { id: "rules", name: "中国场景规则集", role: "规则", fmt: "", ver: "2026.10", builtin: true, on: true },
  { id: "ofd", name: "OFD", role: "格式", fmt: ".ofd", ver: "0.4.2", src: "github.com/lumen-labs/tacet-ofd", commit: "3f9c2a1", on: true, trusted: false, refs: 1 },
  { id: "fin", name: "金融行业规则包", role: "规则", fmt: "", ver: "1.2.0", src: "gitee.com/fintools/tacet-fin-rules", commit: "a91d3e0", on: true, trusted: false, refs: 0 },
];

export const seedSettings = (): Settings => ({ redact: "black", tier: "comfort", autoUpd: false, keep: "7", closeAct: "tray" });
