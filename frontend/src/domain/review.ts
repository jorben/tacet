// Human review of batch exceptions: the item queue, decisions, undo, and the synthetic documents shown for them.
import { current, isDraft, type Draft } from "immer";
import { CLAUSE_NUMERALS, DOCUMENT_TITLES, FILLER_SENTENCES, GIVEN_NAMES, SURNAMES } from "./catalog";
import { countFiles, groupMembers, isExported, isSettled } from "./batch";
import { fmt, locStr } from "./format";
import { createRng, randInt } from "./random";
import type { Batch, BatchFile, ExceptionGroup, FileStatus, ReviewItem, ReviewRecord, ReviewSnapshot, ReviewState } from "./types";

const plain = <T,>(v: T): T => (isDraft(v) ? (current(v as Draft<T>) as T) : v);

export const decisionLabel = (g: ExceptionGroup, k: string): string =>
  k === "pwd" ? "已解锁" : (g.acts.find((a) => a.k === k) || { l: "" }).l;

export function ensureReview(b: Batch): ReviewState {
  return (b.rv ||= { items: [], cur: null, all: false, undo: [], hov: null, fin: false });
}

/** Rebuilds the item queue from the groups; decided items stay. Returns whether the queue changed. */
export function syncReview(b: Batch): boolean {
  const rv = ensureReview(b);
  const want = new Map<string, { gid: string; gi: number; fi: number; j: number }>();
  b.groups.forEach((g, gi) =>
    groupMembers(b, g).forEach((fi) => {
      for (let j = 0; j < g.per[fi]!; j++) want.set(g.id + "|" + fi + "|" + j, { gid: g.id, gi, fi, j });
    }),
  );
  const items = rv.items.filter((it) => it.dec || want.has(it.key));
  const have = new Set(items.map((it) => it.key));
  want.forEach((v, key) => {
    if (!have.has(key)) items.push({ key, dec: null, p: 1 + ((v.fi * 7 + v.j * 5) % Math.max(1, b.files[v.fi]!.pages)), ...v });
  });
  items.sort((a, c) => a.gi - c.gi || a.fi - c.fi || a.p - c.p || a.j - c.j);
  const sig = items.map((it) => it.key + ":" + (it.dec || "")).join(",");
  const changed = sig !== rv.sig;
  // An unchanged queue keeps its identity so nothing that reads it has to update.
  if (changed) {
    rv.items = items;
    rv.sig = sig;
  }
  // Once the review is finished, new exceptions are announced on the card instead of replacing it.
  if (rv.cur == null ? !rv.fin : !rv.items.some((it) => it.key === rv.cur)) {
    const n = rv.items.find((it) => !it.dec);
    rv.cur = n ? n.key : null;
  }
  return changed;
}

/** Marks the review as finished when no item is left to show. Call after every change of the queue. */
export function settleReview(b: Batch): void {
  const rv = ensureReview(b);
  if (!currentItem(b) && !rv.fin) {
    rv.fin = true;
    rv.finAt = Date.now();
    rv.setAt = 0;
  }
}

export function currentItem(b: Batch): ReviewItem | null {
  const rv = b.rv;
  return rv ? rv.items.find((x) => x.key === rv.cur) || null : null;
}

/** Opens the review at the first pending item (of a group and/or a file). */
export function enterReview(b: Batch, gid?: string | null, fi?: number | null): void {
  if (b.rv) b.rv.fin = false;
  syncReview(b);
  const rv = b.rv!;
  const it = rv.items.find((x) => !x.dec && (!gid || x.gid === gid) && (fi == null || x.fi === fi));
  if (it) rv.cur = it.key;
  b.phase = "review";
  settleReview(b);
}

export function selectItem(b: Batch, key: string): void {
  const rv = b.rv!;
  rv.cur = key;
  rv.hov = null;
  rv.fin = false;
}

export function resumeReview(b: Batch): void {
  const rv = b.rv!;
  rv.fin = false;
  rv.hov = null;
  syncReview(b);
}

export function stepItem(b: Batch, d: number): void {
  const rv = b.rv!;
  rv.hov = null;
  rv.fin = false;
  const n = rv.items.length;
  if (!n) return;
  let i = rv.items.findIndex((x) => x.key === rv.cur);
  i = i < 0 ? 0 : (i + d + n) % n;
  rv.cur = rv.items[i]!.key;
}

function nextPending(b: Batch, key: string): ReviewItem | null {
  const its = b.rv!.items;
  const i = its.findIndex((x) => x.key === key);
  for (let k = 1; k <= its.length; k++) {
    const x = its[(i + k) % its.length]!;
    if (!x.dec) return x;
  }
  return null;
}

function snapshot(b: Batch, items: ReviewItem[]): ReviewSnapshot {
  const rv = b.rv!;
  const fis = [...new Set(items.map((t) => t.fi))];
  return {
    cur: rv.cur,
    decs: items.map((t) => [t.key, t.dec]),
    files: fis.map((fi) => {
      const f = b.files[fi]!;
      return { fi, st: f.st, g: [...f.g], partial: f.partial, t: f.t, man: f.man };
    }),
    done: b.groups.map((x) => x.done.map((d) => ({ ...plain(d) }))),
  };
}

function addRecord(g: ExceptionGroup, k: string, n: number): void {
  let rec = g.done.find((d) => d.rv && d.k === k);
  if (!rec) {
    rec = { k, l: decisionLabel(g, k), n: 0, rv: true } as ReviewRecord;
    g.done.push(rec);
    rec = g.done[g.done.length - 1]!;
  }
  rec.n += n;
}

export type DecideResult =
  | { ok: true; toast: string }
  | { ok: false; toast?: string }
  | null;

/** Decides the current item (or, with “本组其余一起”, every pending item of its group). Changing a decision is allowed until the file is exported. */
export function decide(b: Batch, k: string): DecideResult {
  const rv = b.rv!;
  const cur = currentItem(b);
  if (!cur || cur.dec === k) return null;
  const g = b.groups[cur.gi]!;
  if (cur.dec) {
    const f = b.files[cur.fi]!;
    if (isExported(f.st)) return { ok: false, toast: "已导出，不能更改" };
    rv.undo.push(snapshot(b, [cur]));
    const o = g.done.find((d) => d.rv && d.k === cur.dec);
    if (o) { o.n--; if (!o.n) g.done.splice(g.done.indexOf(o), 1); }
    addRecord(g, k, 1);
    cur.dec = k;
    if (k === "redo") f.redo = true;
    const mine = rv.items.filter((x) => x.fi === cur.fi && x.dec);
    f.partial = mine.some((x) => b.groups[x.gi]!.gap && x.dec === "keep");
    if (mine.some((x) => x.dec === "skip")) f.st = "skipped";
    else if (f.st === "skipped") {
      if (f.g.length) f.st = b.mode === "auto" ? "held" : "wait";
      else { f.st = "proc"; f.t = randInt(8, 14); }
    }
    rv.hov = null;
    syncReview(b);
    settleReview(b);
    return { ok: true, toast: "已改为 " + decisionLabel(g, k) };
  }
  const targets = rv.all && k !== "pwd" && !g.noBulk ? rv.items.filter((x) => !x.dec && x.gid === cur.gid) : [cur];
  const fis = [...new Set(targets.map((t) => t.fi))];
  rv.undo.push(snapshot(b, targets));
  targets.forEach((t) => { t.dec = k; });
  addRecord(g, k, targets.length);
  fis.forEach((fi) => {
    const its = rv.items.filter((x) => x.gid === g.id && x.fi === fi);
    if (its.some((x) => !x.dec)) return;
    const f = b.files[fi]!;
    f.g = f.g.filter((x) => x !== g.id);
    f.man = true;
    if (its.some((x) => x.dec === "skip")) { f.st = "skipped"; return; }
    if (g.gap && its.some((x) => x.dec === "keep")) f.partial = true;
    if (k === "redo") f.redo = true;
    if (!f.g.length) { f.st = "proc"; f.t = randInt(8, 14); }
  });
  rv.all = false;
  rv.hov = null;
  const nx = nextPending(b, cur.key);
  syncReview(b);
  rv.cur = nx && !nx.dec && rv.items.some((x) => x.key === nx.key) ? nx.key : rv.items.find((x) => !x.dec)?.key || null;
  settleReview(b);
  return { ok: true, toast: decisionLabel(g, k) + (targets.length > 1 ? " " + targets.length + " " + g.unit : "") };
}

/** Undo of the latest decision. */
export function undoDecision(b: Batch): { ok: boolean; toast: string } | null {
  const rv = b.rv;
  const s = rv && rv.undo.pop();
  if (!rv || !s) return null;
  if (s.files.some(({ fi }) => isExported(b.files[fi]!.st))) return { ok: false, toast: "已导出的文件不能撤销" };
  s.files.forEach(({ fi, st, g, partial, t, man }) => Object.assign(b.files[fi]!, { st, g: [...g], partial, t, man }));
  b.groups.forEach((g, i) => { g.done = s.done[i]!.map((d) => ({ ...d })); });
  const m = new Map(s.decs);
  rv.items.forEach((it) => { if (m.has(it.key)) it.dec = m.get(it.key)!; });
  rv.cur = s.cur;
  rv.fin = false;
  rv.hov = null;
  syncReview(b);
  settleReview(b);
  return { ok: true, toast: "已撤销" };
}

/* ── Presentation helpers ─────────────────────────────────────────────────── */

/** Demo: contracts have numbered clauses as headings; every fourth DOCX was saved without page-break hints. */
export function reviewLocation(f: Pick<BatchFile, "ext" | "name" | "i">, it: Pick<ReviewItem, "p" | "j">): string {
  if (f.ext !== "docx") return "第 " + it.p + " 页";
  const sec = /^(劳动|采购)合同/.test(f.name) ? "第" + CLAUSE_NUMERALS[(it.p + it.j) % 9] + "条" : "";
  return locStr("docx", f.i % 4 ? it.p : null, sec, 3 + ((it.p * 7 + it.j * 5) % 40));
}

export function reviewWhere(g: ExceptionGroup, f: BatchFile, it: ReviewItem): string {
  if (g.id === "pwd") return "已加密";
  if (g.id === "fail") return "整份文件";
  if (g.id === "scan") return "全部 " + f.pages + " 页";
  if (g.id === "strict") return "命中 " + fmt(Object.values(f.h).reduce((a, b) => a + b, 0)) + " 处";
  return reviewLocation(f, it) + (g.per[it.fi]! > 1 ? " · 第 " + (it.j + 1) + " " + g.unit : "");
}

export interface ReviewDocumentData {
  kind: string;
  id: string;
  org: string;
  name: string;
  phone: string;
  email: string;
  addr: string;
  callee: string;
  cphone: string;
  att: string;
  kb: number;
  paras: string[];
  scan: number[];
}

/** Synthetic content of the document an exception points into; deterministic per item. */
export function reviewDocumentData(f: Pick<BatchFile, "name" | "i">, it: Pick<ReviewItem, "fi" | "j">): ReviewDocumentData {
  const kind = Object.keys(DOCUMENT_TITLES).find((k) => f.name.startsWith(k)) || "客户回访";
  const r = createRng(it.fi * 31 + it.j * 7 + 3);
  const ix = <T,>(a: readonly T[]): T => a[Math.floor(r() * a.length)]!;
  const dg = (n: number) => Array.from({ length: n }, () => Math.floor(r() * 10)).join("");
  const id = "330106" + (1978 + (it.fi % 25)) + "0" + (1 + (it.fi % 9)) + (10 + (it.j % 18)) + dg(3) + "X";
  return {
    kind, id,
    org: "杭州" + ix(["澄音", "启明", "云栖", "望湖"]) + ix(["科技", "贸易", "咨询"]) + "有限公司",
    name: ix(SURNAMES) + ix(GIVEN_NAMES),
    phone: "1" + ix(["38", "39", "86", "58"]) + " " + dg(4) + " " + dg(4),
    email: ix(["li", "wang", "zhang", "chen"]) + dg(3) + "@" + ix(["163.com", "qq.com", "corp.cn"]),
    addr: "杭州市" + ix(["西湖区", "滨江区", "拱墅区"]) + ix(["文三路", "江南大道", "莫干山路"]) + dg(3) + " 号",
    callee: ix(SURNAMES) + "先生",
    cphone: "139 " + dg(4) + " " + dg(4),
    att: ["报价单.xlsx", "签字页.pdf", "名单.csv"][(it.fi + it.j) % 3]!,
    kb: 12 + (it.fi % 80),
    paras: Array.from({ length: 6 }, () => ix(FILLER_SENTENCES) + ix(FILLER_SENTENCES)),
    scan: Array.from({ length: 22 }, () => 62 + Math.floor(r() * 36)),
  };
}

/** The decision shown in the effect preview: hovered option, the item's own decision, or the first option. */
export const previewDecision = (rv: ReviewState, it: ReviewItem, g: ExceptionGroup): string => rv.hov || it.dec || g.acts[0]!.k;

export interface ReviewExportSummary {
  autoPublish: boolean;
  /** Files the reviewer decided on (and did not skip). */
  total: number;
  /** Of those: written out (or, when publishing is manual, validated). */
  ok: number;
  /** Of those: validated and waiting to be written out (only meaningful with automatic publishing). */
  readyMine: number;
  failed: number;
  /** Some reviewed file is still being exported or verified. */
  left: boolean;
  /** Items the validation sent back to the reviewer. */
  pending: number;
  /** Files outside the reviewed ones that are still working. */
  otherBusy: number;
  busy: number;
  readyAll: number;
  settled: boolean;
}

/** The reviewed files are exported in the background after each decision; this describes how far that got. */
export function reviewExportSummary(b: Batch): ReviewExportSummary {
  const rv = b.rv!;
  const c = countFiles(b.files);
  const ap = b.autoPub;
  const mine = [...new Set(rv.items.filter((x) => x.dec && x.dec !== "skip").map((x) => x.fi))].filter((fi) => b.files[fi]!.st !== "skipped");
  const n = (sts: FileStatus[]) => mine.filter((fi) => sts.includes(b.files[fi]!.st)).length;
  const out = n(["done", "partial"]);
  const readyMine = n(["ready"]);
  const run = n(["queue", "proc"]);
  const failed = n(["bad"]);
  const ok = ap ? out : out + readyMine;
  const busy = c.queue + c.proc;
  return {
    autoPublish: ap,
    total: mine.length,
    ok,
    readyMine,
    failed,
    left: mine.length - ok - failed - n(["wait", "held"]) > 0,
    pending: rv.items.filter((x) => !x.dec).length,
    otherBusy: busy - run,
    busy,
    readyAll: c.ready,
    settled: isSettled(b),
  };
}
