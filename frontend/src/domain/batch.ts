// The batch state machine. Functions here are pure with respect to the application: they read and change the
// batch they are given (usually an immer draft) and never touch the store, the DOM or timers.
import { GROUP_DEFINITIONS, STRICT_GROUP_ID, SUPPORTED_EXTENSIONS } from "./catalog";
import { extOf, fmt } from "./format";
import { pick, randInt, reseed } from "./random";
import type {
  Batch, BatchFile, BatchSummary, ExceptionGroup, FileFilter, FileStatus, KeepDays, ManifestFile, ReviewRecord,
} from "./types";

export type FileCounts = Record<"queue" | "proc" | "wait" | "held" | "ready" | "done" | "partial" | "bad" | "skip", number>;

const COUNT_KEY: Record<FileStatus, keyof FileCounts> = {
  queue: "queue", proc: "proc", wait: "wait", held: "held", ready: "ready", done: "done", partial: "partial", bad: "bad", skipped: "skip",
};

export function countFiles(files: { st: FileStatus }[]): FileCounts {
  const c: FileCounts = { queue: 0, proc: 0, wait: 0, held: 0, ready: 0, done: 0, partial: 0, bad: 0, skip: 0 };
  files.forEach((f) => c[COUNT_KEY[f.st]]++);
  return c;
}

export const exportedCount = (c: FileCounts): number => c.done + c.partial;

/* ── Creation ─────────────────────────────────────────────────────────────── */

export interface NewBatchOptions {
  title?: string;
  preset: string;
  modelId: string | null;
}

/** Builds a batch from file names. The numbers are synthetic but deterministic for the same list of names. */
export function createBatch(names: string[], opt: NewBatchOptions): Batch {
  reseed(names.length * 7 + 3);
  const n = names.length;
  const files: BatchFile[] = names.map((name, i) => {
    const ext = extOf(name);
    const supported = SUPPORTED_EXTENSIONS.includes(ext);
    const pages = randInt(1, 36);
    const h: BatchFile["h"] = {
      organization: randInt(1, 12), person: randInt(4, 40), phone: randInt(1, 14), email: randInt(0, 6),
      id_number: randInt(0, 4), address: randInt(0, 7), payment_card: randInt(0, 2), date: randInt(1, 10),
    };
    const imgs = randInt(0, 5);
    return {
      i, id: "T" + String(i + 1).padStart(4, "0"), name, ext, pages, h, imgs, st: supported ? "queue" : "bad", t: 0,
      g: [], late: [], det: false, partial: false, man: false, reason: supported ? undefined : "格式不支持",
    };
  });
  // Precheck failures. A very large batch contains one damaged file and one PDF over the page limit
  // (rejected with the actual count; there is no splitting in the MVP).
  if (n >= 20) {
    const f = files[randInt(0, n - 1)]!;
    if (f.st !== "bad") { f.st = "bad"; f.reason = "文件已损坏"; }
  }
  if (n >= 20) {
    const c = files.filter((f) => f.st !== "bad" && f.ext === "pdf");
    if (c.length) {
      const f = c[randInt(0, c.length - 1)]!;
      f.pages = 2340; f.st = "bad"; f.reason = "超过 2,000 页 · 实际 2,340 页";
    }
  }
  const groups: ExceptionGroup[] = [];
  const avail = files.filter((f) => f.st !== "bad");
  GROUP_DEFINITIONS.forEach((d) => {
    const base = { id: d.id, ic: d.ic, t: d.t, unit: d.unit, block: d.block, gap: d.gap, post: d.post, acts: d.acts.map((a) => ({ ...a })), done: [] };
    if (d.after) { groups.push({ ...base, per: {} }); return; }
    if (n < 10 && !["shot", "idck"].includes(d.id)) return;
    const pool = d.ext ? avail.filter((f) => f.ext === d.ext) : avail;
    if (!pool.length) return;
    const k = Math.max(d.min || 1, Math.round(n * d.frac!));
    const per: Record<number, number> = {};
    for (let j = 0; j < k && j < avail.length * 2; j++) {
      const f = pick(pool);
      if (per[f.i] != null) continue;
      if (d.unit === "份" && Object.values(per).length && groups.some((g) => g.unit === "份" && g.per[f.i] != null)) continue;
      per[f.i] = d.perRange ? randInt(d.perRange[0], d.perRange[1]) : 1;
    }
    Object.keys(per).forEach((fi) => files[+fi]!.g.push(d.id));
    groups.push({ ...base, per });
  });
  // New hits found by output validation join their category group.
  if (n >= 10) {
    groups.filter((g) => g.post).forEach((g) => {
      g.found = {};
      const c = avail.filter((f) => g.per[f.i] == null && !f.late.length);
      for (let j = 0; j < Math.max(1, Math.round(n * g.post!)) && c.length; j++) {
        const f = c.splice(randInt(0, c.length - 1), 1)[0]!;
        g.per[f.i] = 1; g.found[f.i] = true; f.late.push(g.id);
      }
    });
  }
  // Validation outcomes: not completed → auto re-run; residue → auto widen; broken output → whole-file failure.
  if (n >= 20) {
    const c = avail.filter((f) => !f.late.length);
    const take = (fn: (f: BatchFile) => boolean): BatchFile | null => {
      const xs = c.filter(fn);
      if (!xs.length) return null;
      const f = xs[randInt(0, xs.length - 1)]!;
      c.splice(c.indexOf(f), 1);
      return f;
    };
    [1, 1, 3].forEach((v) => { const f = take((x) => !x.g.length); if (f) f.vr = v; });
    for (let j = 0; j < 2; j++) {
      const f = take(() => true);
      if (f) { f.res = true; f.resN = randInt(1, 2); }
    }
    const b = take((f) => f.ext === "docx");
    if (b) b.brk = true;
  }
  return {
    title: opt.title || "新批次", n, files, groups, phase: "precheck", mode: "guided", preset: opt.preset, modelId: opt.modelId,
    imgAct: "remove", out: "桌面 / 已脱敏", autoPub: true, keep: "7", paused: false, tab: "todo", ff: "all", q: "",
  };
}

/** Moves a prechecked batch into the run phase. Strict mode adds a per-file confirmation group. */
export function beginRun(b: Batch): void {
  b.phase = "run";
  if (b.mode === "auto") b.autoPub = true;
  b.files.forEach((f) => { if (f.st === "bad") f.det = true; });
  // Strict: every file waits for an explicit per-file confirmation, never bulk-confirmable (BUX-08).
  if (b.mode === "strict" && !b.groups.some((g) => g.id === STRICT_GROUP_ID)) {
    const per: Record<number, number> = {};
    b.files.forEach((f) => { if (f.st !== "bad") { per[f.i] = 1; f.g.push(STRICT_GROUP_ID); } });
    b.groups.push({
      id: STRICT_GROUP_ID, ic: "eye", t: "逐份确认", unit: "份", noBulk: true, per, done: [],
      acts: [{ l: "确认导出", k: "ok" }, { l: "跳过此文件", k: "skip", n: "不导出" }],
    });
  }
}

/* ── Queries ──────────────────────────────────────────────────────────────── */

/** Files that still wait in the group (skipped ones and, by default, files not yet detected are left out). */
export function groupMembers(b: Batch, g: ExceptionGroup, detectedOnly = true): number[] {
  return Object.keys(g.per).map(Number).filter((fi) => {
    const f = b.files[fi]!;
    return f.g.includes(g.id) && f.st !== "skipped" && (!detectedOnly || f.det);
  });
}
export const openGroups = (b: Batch): ExceptionGroup[] => b.groups.filter((g) => groupMembers(b, g).length);
export const groupTotal = (g: ExceptionGroup, members: number[]): number => members.reduce((a, fi) => a + g.per[fi]!, 0);
export const pendingItems = (b: Batch): number => openGroups(b).reduce((a, g) => a + groupTotal(g, groupMembers(b, g)), 0);

/** Nothing is queued, running or waiting for export: only what needs a person is left. */
export function machineDone(b: Pick<Batch, "files">): boolean {
  const c = countFiles(b.files);
  return !c.queue && !c.proc && !c.wait && !c.ready;
}
export function isSettled(b: Pick<Batch, "files" | "phase">): boolean {
  const c = countFiles(b.files);
  return !c.queue && !c.proc && !c.wait && !c.ready && !(b.phase === "review" && c.held);
}

export type StatusKind = "idle" | "warn" | "run" | "done" | "part";
/** One status vocabulary for home, the rail dot and the close guards: [kind, label]. */
export function batchStatus(b: Batch): [StatusKind, string] {
  const c = countFiles(b.files);
  const dn = exportedCount(c);
  const N = fmt(b.n);
  if (b.phase === "precheck") return ["idle", "待开始"];
  if (!isSettled(b)) {
    const p = pendingItems(b);
    if (p && b.mode !== "auto") return ["warn", "待你处理 " + fmt(p) + " 处"];
    if (c.queue + c.proc) return ["run", (b.paused ? "已暂停" : "处理中") + " · " + fmt(dn) + " / " + N];
    if (c.ready && !b.autoPub) return ["warn", "待导出 " + fmt(c.ready) + " 份"];
    if (p) return ["warn", "待你处理 " + fmt(p) + " 处"];
    return ["run", "处理中 · " + fmt(dn) + " / " + N];
  }
  if (c.held) return ["warn", "已导出 " + fmt(dn) + " / " + N + " · 待你处理 " + fmt(c.held) + " 份"];
  return dn === b.n ? ["done", "已导出 " + N] : ["part", "已导出 " + fmt(dn) + " / " + N];
}

/** Whether a file shows under a filter chip and a search text (by name or by number). */
export function matchesFileFilter(f: Pick<BatchFile, "name" | "id" | "st">, filter: FileFilter, query: string): boolean {
  const inQuery = !query || f.name.includes(query) || f.id.includes(query.toUpperCase());
  const st = f.st;
  const inFilter =
    filter === "all" ||
    (filter === "done" && (st === "done" || st === "partial")) ||
    (filter === "wait" && (st === "wait" || st === "held")) ||
    (filter === "bad" && (st === "bad" || st === "skipped")) ||
    (filter === "run" && (st === "queue" || st === "proc" || st === "ready"));
  return inQuery && inFilter;
}

export function etaLabel(c: FileCounts): string {
  const remaining = c.queue + c.proc;
  if (!remaining) return "";
  const lo = Math.max(1, Math.round((remaining * 0.9) / 60));
  const hi = Math.max(lo + 1, Math.round((remaining * 1.6) / 60));
  return "剩余约 " + lo + "–" + hi + " 分钟";
}

export function keepDays(b: { keep: KeepDays } | null | undefined): KeepDays {
  return b && b.keep !== "0" ? b.keep : "7";
}

/** A stable stand-in for the SHA-256 column of the manifest. */
export function fakeHash(i: number): string {
  let x = ((i + 1) * 2654435761) >>> 0;
  let s = "";
  for (let k = 0; k < 3; k++) {
    x = (x * 1103515245 + 12345) >>> 0;
    s += x.toString(16).padStart(8, "0").slice(0, 4);
  }
  return s + "…";
}

export const isExported = (st: FileStatus): boolean => st === "done" || st === "partial";

/** What the result page and manifest need, from a batch or an archived summary. */
export function summaryCounts(b: Pick<BatchSummary, "files">): FileCounts {
  return countFiles(b.files as { st: FileStatus }[]);
}

export function manifestRows(b: BatchSummary): ManifestFile[] {
  const k = b.manifestFilter || "all";
  return b.files.filter((f) => k === "all" || (k === "ok" && isExported(f.st)) || (k === "no" && !isExported(f.st)));
}

/* ── Processing ───────────────────────────────────────────────────────────── */

/** Puts a file back to work for a validation step (re-verify, widen redaction, re-export). */
export function markBusy(f: BatchFile, why: string): void {
  f.st = "proc";
  f.t = randInt(6, 10);
  f.reason = why;
  f.tmp = true;
}

/** Whole-file failure: the file joins the “处理失败” group. */
export function markFailed(b: Batch, f: BatchFile, why: string): void {
  f.failWhy = why;
  b.groups.find((g) => g.id === "fail")!.per[f.i] = 1;
  f.g.push("fail");
  f.st = b.mode === "auto" ? "held" : "wait";
}

/** A file finished its processing step: it joins an exception group, is re-validated, or becomes ready to export. */
export function finishProcessing(b: Batch, f: BatchFile): void {
  f.det = true;
  if (f.tmp) { f.reason = null; f.tmp = false; }
  if (f.late.length && !f.g.length) { f.late.forEach((x) => f.g.push(x)); f.late = []; }
  if (f.g.length) { f.st = b.mode === "auto" ? "held" : "wait"; return; }
  // Validation not completed: re-run automatically within budget, never queued for review.
  if (f.vr) {
    f.vr--;
    f.vn = (f.vn || 0) + 1;
    if (f.vn > 2) { f.vr = 0; markFailed(b, f, "验证超时，已自动重试 2 次"); return; }
    markBusy(f, "重新验证");
    return;
  }
  // Residue under an approved redaction: widen within the same text unit, no human decision.
  if (f.res) { f.res = false; b.widened = (b.widened || 0) + (f.resN || 0); markBusy(f, "扩大涂抹范围"); return; }
  // Output cannot be reopened: whole-file failure → reprocess / skip; failing again → cannot be processed.
  if (f.brk) {
    if (f.redo) { f.st = "bad"; f.reason = "重新处理后仍失败"; return; }
    markFailed(b, f, "导出文件无法打开 · 页眉 2 结构无效");
    return;
  }
  f.st = "ready";
  f.rt = b.autoPub ? randInt(1, 3) : null;
}

/** Advance the simulated clock of a running batch by one step: finish work, publish, dispatch new files. */
export function advanceFiles(b: Batch): void {
  b.files.forEach((f) => {
    if (f.st === "proc") {
      f.t--;
      if (f.t <= 0) finishProcessing(b, f);
    } else if (f.st === "ready" && b.autoPub && f.rt != null) {
      f.rt--;
      if (f.rt <= 0) f.st = f.partial ? "partial" : "done";
    }
  });
  if (!b.paused) {
    let cap = 10 - b.files.filter((f) => f.st === "proc").length;
    for (const f of b.files) {
      if (cap <= 0) break;
      if (f.st === "queue") { f.st = "proc"; f.t = randInt(2, 6); cap--; }
    }
  }
}

/* ── Resolving exception groups ───────────────────────────────────────────── */

let recordUid = 0;
export interface GroupResolution {
  label: string;
  n: number;
  unit: string;
  /** Restores the files and the group record as they were before the resolution. */
  restore: ResolutionSnapshot;
  recordUid: number;
}
interface ResolutionSnapshot {
  files: { fi: number; st: FileStatus; g: string[]; partial: boolean; t: number }[];
  gid: string;
  uid: number;
}

/** Applies one action (“全部涂抹”, “全部保留”, “解锁”, …) to every file still waiting in the group. */
export function resolveGroup(b: Batch, gid: string, k: string, only?: number[]): GroupResolution | null {
  const g = b.groups.find((x) => x.id === gid)!;
  const mem = only || groupMembers(b, g);
  if (!mem.length) return null;
  const act = k === "pwd" ? { l: "已解锁" } : g.acts.find((a) => a.k === k) || { l: "处理" };
  const files = mem.map((fi) => {
    const f = b.files[fi]!;
    return { fi, st: f.st, g: [...f.g], partial: f.partial, t: f.t };
  });
  mem.forEach((fi) => {
    const f = b.files[fi]!;
    f.g = f.g.filter((x) => x !== gid);
    f.man = true;
    if (k === "skip") { f.st = "skipped"; f.g = []; return; }
    if (k === "keep" && g.gap) f.partial = true;
    if (k === "redo") f.redo = true;
    if (!f.g.length) { f.st = "proc"; f.t = randInt(14, 22); }
  });
  const uid = ++recordUid;
  const rec: ReviewRecord = { k, l: act.l, n: groupTotal(g, mem), files: mem.length, mem, uid };
  g.done.push(rec);
  return { label: act.l, n: rec.n, unit: g.unit, restore: { files, gid, uid }, recordUid: uid };
}

/** Undo of `resolveGroup`. Returns false when a file was exported in the meantime. */
export function undoResolution(b: Batch, r: GroupResolution): boolean {
  const { files, gid, uid } = r.restore;
  if (files.some(({ fi }) => isExported(b.files[fi]!.st))) return false;
  files.forEach(({ fi, st, g, partial, t }) => Object.assign(b.files[fi]!, { st, g: [...g], partial, t }));
  const g = b.groups.find((x) => x.id === gid)!;
  g.done = g.done.filter((x) => x.uid !== uid);
  return true;
}

/** Writes every validated file out at once (manual publishing). */
export function publishReady(b: Batch): number {
  const n = countFiles(b.files).ready;
  b.files.forEach((f) => { if (f.st === "ready") f.st = f.partial ? "partial" : "done"; });
  return n;
}
