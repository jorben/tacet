// Mapping between what the real engine reports and the shapes the screens draw. Pure: no store, no service.
import type { EngineFile, EngineFinding, EngineUnit } from "../services/types";
import { CATEGORY_IDS } from "./catalog";
import type { DocLocation,
  Block, BatchFile, CategoryId, DocContent, ExceptionGroup, FileStatus, Hit, LiveSingle, OfficeCategoryId,
} from "./types";

const UI_CATEGORY: Record<string, CategoryId> = {
  phone: "phone", email: "email", id_number: "id", payment_card: "card", date: "date",
  person: "name", address: "addr", organization: "org",
  account: "account", ip_address: "ip_address", url: "url", secret: "secret",
};
const ENGINE_CATEGORY: Record<CategoryId, string> = {
  phone: "phone", email: "email", id: "id_number", card: "payment_card", date: "date",
  name: "person", addr: "address", org: "organization",
  account: "account", ip_address: "ip_address", url: "url", secret: "secret", other: "other",
};

/** The screen category of an engine category; anything without a own place is “其他”. */
export const uiCategory = (c: string): CategoryId => UI_CATEGORY[c] ?? "other";
export const engineCategory = (c: CategoryId): string => ENGINE_CATEGORY[c];

const OFFICE: OfficeCategoryId[] = CATEGORY_IDS.filter((c): c is OfficeCategoryId => c !== "other");
export const zeroHits = (): Record<OfficeCategoryId, number> =>
  Object.fromEntries(OFFICE.map((c) => [c, 0])) as Record<OfficeCategoryId, number>;

const SOURCE_LABEL: Record<string, string> = { rule: "规则", model: "模型", dictionary: "词表", manual: "人工" };

/** Why a file stopped, in words. Codes are closed; an unknown one is shown as it is. */
const REASONS: Record<string, string> = {
  FORMAT_NOT_SUPPORTED: "格式不支持",
  PAGE_LIMIT_EXCEEDED: "超过页数上限",
  SIZE_LIMIT_EXCEEDED: "超过大小上限",
  ENCRYPTION_UNSUPPORTED: "不支持的加密方式",
  SOURCE_UNREADABLE: "无法读取原文件",
  INPUT_COPY_FAILED: "无法读取原文件",
  PROBE_FAILED: "文件无法解析",
  PROBE_INVALID: "文件无法解析",
  PLUGIN_START_FAILED: "处理组件未能启动",
  PLUGIN_HANDSHAKE_FAILED: "处理组件启动后没有应答",
  PASSWORD_INCORRECT: "密码不正确，请重试",
  password_required: "需要密码",
  INPUT_CHANGED: "原文件已变化",
  PUBLISH_NAME_CONFLICT: "输出位置已有同名文件",
  EXPORT_FAILED: "导出失败",
  VALIDATE_FAILED: "输出验证未通过",
  UNLOCK_FAILED: "解锁失败",
  RESOURCE_UNAVAILABLE: "资源暂不可用",
  ACTIVITY_BUSY: "暂时无法处理，请稍后重试",
};
export const reasonLabel = (code: string | undefined): string | null => (code ? (REASONS[code] ?? code) : null);

/* ── Files of a batch ─────────────────────────────────────────────────────── */

export const GROUP_REVIEW = "live-review";
export const GROUP_PASSWORD = "live-password";
export const GROUP_BLOCKED = "live-blocked";
/** Strict mode: each file waits for its own confirmation (never several at once). */
export const GROUP_STRICT = "live-strict";

const PROCESSING = new Set(["preflighting", "queued", "extracting_detecting", "resolving", "exporting", "validating", "publishing", "cancelling", "paused"]);

/** The row status of an engine file. */
export function fileStatus(f: EngineFile): FileStatus {
  switch (f.state) {
    case "completed": return "done";
    case "partial": return "partial";
    case "ready_to_publish": return "ready";
    case "awaiting_review": case "locked": case "blocked": return "held";
    case "failed": case "unprocessable": return "bad";
    case "skipped": case "cancelled": return "skipped";
    default: return PROCESSING.has(f.state) ? "proc" : "queue";
  }
}

const passwordWait = (f: EngineFile): boolean => f.state === "locked" || (f.state === "blocked" && (f.detail === "password_required" || f.detail === "PASSWORD_INCORRECT"));

/** Exceptions of a file other than its strict-mode confirmation. */
const reviewCount = (f: EngineFile): number => f.pending - (f.confirm ? 1 : 0);

/** The exception groups a file waits in: its confirmation (strict mode) and what else it holds. */
export function groupsOf(f: EngineFile): string[] {
  const g = groupOf(f);
  if (!f.confirm || f.state !== "awaiting_review") return g ? [g] : [];
  return reviewCount(f) > 0 ? [GROUP_STRICT, GROUP_REVIEW] : [GROUP_STRICT];
}

/** Which exception group a file waits in (empty: none). */
export function groupOf(f: EngineFile): string | null {
  if (f.state === "awaiting_review") return f.confirm && reviewCount(f) <= 0 ? GROUP_STRICT : GROUP_REVIEW;
  if (passwordWait(f)) return GROUP_PASSWORD;
  if (f.state === "blocked" || f.state === "failed") return GROUP_BLOCKED;
  return null;
}

/** A batch row from an engine row; the identity and the names the person gave stay as they were. */
export function toBatchFile(prev: BatchFile, f: EngineFile): BatchFile {
  const h = zeroHits();
  let hits = 0;
  for (const [c, n] of Object.entries(f.categories ?? {})) {
    const k = uiCategory(c);
    hits += n ?? 0;
    if (k !== "other") h[k] += n ?? 0;
  }
  const st = fileStatus(f);
  const group = groupOf(f);
  const groups = groupsOf(f);
  const reason = st === "bad" || st === "held" ? reasonLabel(f.detail) : null;
  return {
    ...prev,
    h,
    st,
    det: hits > 0 || !["preflighting", "preflighted", "queued", "extracting_detecting"].includes(f.state),
    g: groups,
    reason: group === GROUP_REVIEW || group === GROUP_STRICT ? null : reason,
    outputName: f.output || undefined,
    sha: f.digest ?? "",
    partial: f.state === "partial",
  };
}

/** The exception groups a batch's files wait in. Rows are fixed by the files; `noBulk` because decisions are per file. */
export function liveGroups(files: BatchFile[], rows: EngineFile[]): ExceptionGroup[] {
  const defs: Record<string, Omit<ExceptionGroup, "per" | "done">> = {
    [GROUP_STRICT]: { id: GROUP_STRICT, ic: "eye", t: "逐份确认", unit: "份", noBulk: true, acts: [{ l: "去确认", k: "open" }] },
    [GROUP_REVIEW]: { id: GROUP_REVIEW, ic: "eye", t: "需要复核", unit: "处", noBulk: true, acts: [{ l: "去复核", k: "open" }] },
    [GROUP_PASSWORD]: { id: GROUP_PASSWORD, ic: "lock", t: "需要密码", unit: "份", noBulk: true, acts: [{ l: "输入密码", k: "pwd" }] },
    [GROUP_BLOCKED]: { id: GROUP_BLOCKED, ic: "retry", t: "处理受阻", unit: "份", block: true, noBulk: true, acts: [{ l: "重新处理", k: "retry" }] },
  };
  const per: Record<string, Record<number, number>> = {};
  rows.forEach((r, i) => {
    if (!files[i]) return;
    for (const g of groupsOf(r)) (per[g] ||= {})[i] = g === GROUP_REVIEW ? Math.max(1, reviewCount(r)) : 1;
  });
  return Object.keys(defs).filter((id) => per[id]).map((id) => ({ ...defs[id]!, per: per[id]!, done: [] }));
}

/* ── Documents ────────────────────────────────────────────────────────────── */

const pageOf = (part: string): number => {
  const m = /^page\/(\d+)/.exec(part);
  return m ? Math.max(0, Number(m[1]) - 1) : 0;
};

export interface LiveDocument extends DocContent {
  findingOf: number[];
  seqOf: number[][];
}

/**
 * Pages of blocks, hits and the links back to the engine from engine units. A unit is one block; findings
 * that overlap an earlier one in the same unit are left out of the text (the engine still decides them).
 * `previous` carries the person's choices over when a document is loaded again after a change.
 */
export function buildLiveDocument(units: EngineUnit[], ext: string, previous?: { findingOf: number[]; hits: Hit[] }): LiveDocument {
  const old = new Map<number, Hit>();
  previous?.findingOf.forEach((f, i) => { if (previous.hits[i]) old.set(f, previous.hits[i]!); });
  const pages: Block[][] = [];
  const seqOf: number[][] = [];
  const hits: Hit[] = [];
  const findingOf: number[] = [];
  let para = 0;
  const texts = new Map<string, string>(); // part#ordinal -> text, to name a section by its heading
  for (const u of units) {
    const page = pageOf(u.part);
    while (pages.length <= page) { pages.push([]); seqOf.push([]); }
    para++;
    texts.set(u.part + "#" + u.ordinal, u.text);
    const at: DocLocation = ext === "docx" ? docxAt(u, page, texts) : { page, para, sec: "" };
    const segs: NonNullable<Block["segs"]> = [];
    let last = 0;
    const fs = [...(u.findings ?? [])].sort((a, b) => a.start - b.start || b.end - a.end);
    for (const f of fs) {
      if (f.start < last || f.end <= f.start || f.end > u.text.length) continue;
      if (f.start > last) segs.push({ t: u.text.slice(last, f.start) });
      const prev = old.get(f.id);
      hits.push(hitOf(f, u.text.slice(f.start, f.end), at, hits.length, prev));
      findingOf.push(f.id);
      segs.push({ h: hits.length - 1 });
      last = f.end;
    }
    if (last < u.text.length) segs.push({ t: u.text.slice(last) });
    pages[page]!.push({ k: u.visibility && u.visibility !== "visible" ? "meta" : ext === "docx" ? "pi" : "p", segs, at });
    seqOf[page]!.push(u.seq);
  }
  if (!pages.length) { pages.push([]); seqOf.push([]); }
  return { pages, hits, imgs: [], findingOf, seqOf };
}

const AREA: [RegExp, string][] = [[/header\d*\.xml$/, "页眉"], [/footer\d*\.xml$/, "页脚"], [/footnotes\.xml$/, "脚注"], [/endnotes\.xml$/, "尾注"]];

/** A DOCX unit's place (requirements 4.3): approximate page when the file was saved with page markers, the
 *  nearest heading, the paragraph's own ordinal, a table cell, and the area (header, footnote…) it is in. */
function docxAt(u: EngineUnit, page: number, texts: Map<string, string>): DocLocation {
  const l = u.location ?? {};
  const area = AREA.find(([re]) => re.test(u.part))?.[1] ?? "";
  const heading = l.section ? (texts.get(u.part + "#" + l.section) ?? "").trim().slice(0, 24) : "";
  const at: DocLocation = { page, para: u.ordinal, sec: [area, heading].filter(Boolean).join(" "), hint: l.page_hint ?? 0 };
  if (l.table) at.cell = "表 " + l.table.index + " 第 " + l.table.row + " 行第 " + l.table.col + " 列";
  return at;
}

function hitOf(f: EngineFinding, text: string, at: DocLocation, id: number, prev?: Hit): Hit {
  const type = uiCategory(f.category);
  const manual = f.source === "manual";
  const src = f.low ? (SOURCE_LABEL[f.source] ?? f.source) + " · 低置信" : (SOURCE_LABEL[f.source] ?? f.source);
  return {
    id, type, text, low: f.low, seen: prev?.seen ?? manual, src, ...at, manual: manual || undefined,
    state: prev?.state ?? (f.decision === "keep" ? "keep" : "redact"),
  };
}

/** Where a selection inside a block lies in its unit's text: UTF-16 offsets, as the engine counts them. */
export function unitOffsets(block: Block, hits: Hit[], segment: number, st: number, en: number): [number, number] {
  let before = 0;
  (block.segs ?? []).slice(0, segment).forEach((g) => { before += g.h != null ? hits[g.h]!.text.length : g.t.length; });
  return [before + st, before + en];
}

/** The decisions to hand to the engine for what is shown: keep or redact for every hit. */
export function decisionsOf(hits: Hit[], live: Pick<LiveSingle, "findingOf">): { id: number; keep: boolean }[] {
  return hits.map((h, i) => ({ id: live.findingOf[i]!, keep: h.state === "keep" })).filter((d) => d.id != null);
}
