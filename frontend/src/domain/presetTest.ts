// Scheme test: what a scheme finds in a text the person types, and where each finding comes from.
// Pure: the kernel's result (desktop) or a simulation of it (browser) goes in, the screen's shape comes out.
import type { EnginePresetTest } from "../services/types";
import type { CategoryId, ModelPlugin, Preset, Rule, Ruleset } from "./types";
import { categoryName, STANDARD_CATEGORIES, type CategoryInfo } from "./categories";
import { effectiveLabels } from "./liveConfig";
import { presetCategories, presetConfidence, presetTreatment } from "./presets";

/** The kernel refuses longer texts; the screen says so before asking. */
export const MAX_TEST_TEXT = 20000;

/** A model finding of a span. */
export interface TestModelSource { score: number; low: boolean; below: boolean }

/** One span of the text and everything that found it as one category. */
export interface TestHit {
  start: number;
  end: number;
  /** Category id (person, phone, account, a user category, …). */
  cat: CategoryId;
  /** Colour of the category on screen: the category's own. */
  color: CategoryId;
  /** The category's catalog name. */
  label: string;
  model: TestModelSource | null;
  /** Names of the rules that matched it; empty when only the model found it. */
  rules: string[];
}

export interface TestResult {
  hits: TestHit[];
  rulesOnly: boolean;
  /** The scheme's minimum confidence; absent for rules-only schemes. */
  minConfidence?: number;
}

export interface TestCounts { total: number; model: number; rules: number; both: number; below: number }

/** “模型”, “规则” or “模型 + 规则”: where a finding comes from. */
export const testSourceLabel = (h: Pick<TestHit, "model" | "rules">): string =>
  h.model ? (h.rules.length ? "模型 + 规则" : "模型") : "规则";

export function testCounts(hits: TestHit[]): TestCounts {
  const c: TestCounts = { total: hits.length, model: 0, rules: 0, both: 0, below: 0 };
  for (const h of hits) {
    if (h.model && h.rules.length) c.both++;
    else if (h.model) c.model++;
    else c.rules++;
    if (h.model?.below && !h.rules.length) c.below++;
  }
  return c;
}

interface RawHit { start: number; end: number; category: string; model?: TestModelSource; rule?: string }

/** One entry per span and category: a span both the model and a rule found is one finding with two sources. */
function merge(raw: RawHit[], catalog: readonly CategoryInfo[]): TestHit[] {
  const by = new Map<string, TestHit>();
  for (const r of raw) {
    const cat = r.category;
    const key = r.start + ":" + r.end + ":" + cat;
    let h = by.get(key);
    if (!h) {
      h = { start: r.start, end: r.end, cat, color: cat, label: categoryName(catalog, cat), model: null, rules: [] };
      by.set(key, h);
    }
    if (r.model && (!h.model || r.model.score > h.model.score)) h.model = r.model;
    if (r.rule != null && !h.rules.includes(r.rule)) h.rules.push(r.rule);
  }
  return [...by.values()].sort((a, b) => a.start - b.start || b.end - a.end || a.cat.localeCompare(b.cat));
}

/** The kernel's answer, in the screen's shape. */
export function testFromEngine(r: EnginePresetTest, catalog: readonly CategoryInfo[] = STANDARD_CATEGORIES): TestResult {
  const raw: RawHit[] = (r.hits ?? []).map((h) => h.source === "model"
    ? { start: h.start, end: h.end, category: h.category, model: { score: h.score ?? 0, low: !!h.low, below: !!h.below } }
    : { start: h.start, end: h.end, category: h.category, rule: h.ruleName || h.rule || (h.source === "dictionary" ? "词表" : "规则") });
  return { hits: merge(raw, catalog), rulesOnly: r.rulesOnly, ...(r.rulesOnly ? {} : { minConfidence: r.minConfidence }) };
}

/* ── browser simulation ─────────────────────────────────────────────────────── */

/** Rules whose check is done by code (“校验”) stand in with a pattern and the same check. */
const CHECKED: Partial<Record<CategoryId, { re: string; ok?: (s: string) => boolean }>> = {
  id_number: { re: "(?<![0-9])\\d{17}[\\dXx](?![0-9A-Za-z])", ok: idChecksum },
  payment_card: { re: "(?<![0-9])\\d{16,19}(?![0-9Xx])", ok: luhn },
  secret: { re: "\\b(?:sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{30,}|AKIA[0-9A-Z]{16})\\b" },
};

/** What the simulated model recognises, per engine category, with the score it reports. */
const MODEL_PATTERNS: Record<string, { re: string; score: number; group?: number; ok?: (s: string) => boolean }> = {
  person: { re: "(?:姓名|联系人|负责人|经办人|申请人|收件人|法定代表人|代表)[:：]\\s*([\\u4e00-\\u9fa5]{2,4})", score: 0.88, group: 1 },
  address: { re: "[\\u4e00-\\u9fa5]{2,6}(?:省|市)[\\u4e00-\\u9fa5]{0,8}?(?:区|县|市)[\\u4e00-\\u9fa5\\d]{1,20}?(?:路|街|道|巷)\\d+号(?:[\\d-]+室)?", score: 0.81 },
  date: { re: "\\d{4}[-/.年]\\d{1,2}[-/.月]\\d{1,2}日?", score: 0.93 },
  phone: { re: "(?<![0-9])1[3-9]\\d{9}(?![0-9])", score: 0.97 },
  email: { re: "[\\w.+-]+@[\\w-]+(?:\\.[\\w-]+)+", score: 0.98 },
  url: { re: "https?://[^\\s，。；、）)]+", score: 0.95 },
  ip_address: { re: "(?<![0-9.])(?:\\d{1,3}\\.){3}\\d{1,3}(?![0-9.])", score: 0.9 },
  secret: { re: "\\b(?:sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{30,}|AKIA[0-9A-Z]{16})\\b", score: 0.9 },
  payment_card: { re: "(?<![0-9])\\d{16,19}(?![0-9Xx])", score: 0.72, ok: luhn },
};

const escape = (s: string): string => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const BOUNDARY: Record<string, [string, string]> = { digit: ["(?<![0-9])", "(?![0-9])"], word: ["(?<![A-Za-z0-9])", "(?![A-Za-z0-9])"] };

function matches(text: string, source: string, group?: number, ok?: (s: string) => boolean): { start: number; end: number }[] {
  let re: RegExp;
  try { re = new RegExp(source, "gu"); } catch { return []; }
  const out: { start: number; end: number }[] = [];
  for (const m of text.matchAll(re)) {
    if (!m[0]) continue;
    let start = m.index!, value = m[0];
    if (group != null && m[group] != null) { start += m[0].indexOf(m[group]); value = m[group]; }
    if (!ok || ok(value)) out.push({ start, end: start + value.length });
  }
  return out;
}

function ruleMatches(r: Rule, text: string): { start: number; end: number }[] {
  if (r.kind === "词表") {
    const terms = r.body.split("\n").map((t) => t.trim()).filter(Boolean);
    return terms.length ? matches(text, terms.map(escape).sort((a, b) => b.length - a.length).join("|")) : [];
  }
  if (r.kind === "校验") {
    const c = CHECKED[r.cat];
    return c ? matches(text, c.re, undefined, c.ok) : [];
  }
  const [before, after] = BOUNDARY[r.bd || ""] ?? ["", ""];
  return matches(text, before + "(?:" + r.body + ")" + after);
}

/**
 * The browser stands in for the kernel: the scheme's active rules run as they are written, the model is a
 * handful of patterns. Categories the scheme turns off are not reported, as in a task.
 */
export function simulateTest(p: Preset, rulesets: Ruleset[], models: ModelPlugin[], text: string, catalog: readonly CategoryInfo[] = STANDARD_CATEGORIES): TestResult {
  const useModel = p.det === "model";
  const on = new Set(presetCategories(p, models, rulesets).filter((c) => !["off", "na"].includes(presetTreatment(p, c))));
  const raw: RawHit[] = [];
  for (const set of rulesets.filter((r) => p.rules.includes(r.id))) {
    for (const r of set.rules) {
      if (!r.on) continue;
      const category = r.cat;
      if (!on.has(category)) continue;
      for (const m of ruleMatches(r, text)) raw.push({ ...m, category, rule: r.name });
    }
  }
  const min = presetConfidence(p, models);
  if (useModel) {
    const model = models.find((m) => m.id === p.model);
    // what a label finds is what it means by default; it is reported as the category it is mapped to now
    const labels = model ? effectiveLabels(model) : {};
    const pairs = model?.labels ? Object.entries(labels).map(([label, category]) => [model.labels![label]!, category] as const) : (model?.categories ?? []).map((c) => [c, c] as const);
    for (const [meaning, category] of pairs) {
      const pat = MODEL_PATTERNS[meaning];
      if (!pat || !on.has(category)) continue;
      for (const m of matches(text, pat.re, pat.group, pat.ok)) {
        raw.push({ ...m, category, model: { score: pat.score, low: pat.score < (model?.confRec ?? 0.5), below: pat.score < min } });
      }
    }
  }
  return { hits: merge(raw, catalog), rulesOnly: !useModel, ...(useModel ? { minConfidence: min } : {}) };
}

function luhn(s: string): boolean {
  let sum = 0;
  for (let i = 0; i < s.length; i++) {
    let d = +s[s.length - 1 - i]!;
    if (i % 2) { d *= 2; if (d > 9) d -= 9; }
    sum += d;
  }
  return sum % 10 === 0;
}

function idChecksum(s: string): boolean {
  const w = [7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2];
  const sum = w.reduce((a, x, i) => a + x * +s[i]!, 0);
  return "10X98765432"[sum % 11] === s[17]!.toUpperCase();
}

/* ── drawing ─────────────────────────────────────────────────────────────────── */

/** A run of the text, and the finding it belongs to (the earliest, longest one where findings overlap). */
export interface TestSegment { text: string; hit: number | null }

export function testSegments(text: string, hits: TestHit[]): TestSegment[] {
  const out: TestSegment[] = [];
  let at = 0;
  hits.forEach((h, i) => {
    if (h.end <= at) return; // inside an earlier finding
    const start = Math.max(h.start, at);
    if (start > at) out.push({ text: text.slice(at, start), hit: null });
    out.push({ text: text.slice(start, h.end), hit: i });
    at = h.end;
  });
  if (at < text.length) out.push({ text: text.slice(at), hit: null });
  return out;
}
