// The batch task: precheck settings, the running clock, exception groups, sampling and opening single files.
import { current, isDraft, type Draft } from "immer";
import {
  advanceFiles, batchStatus, beginRun, countFiles, createBatch, exportedCount, groupMembers, groupTotal, isSettled,
  keepDays, markBusy, machineDone, publishReady, resolveGroup, undoResolution,
} from "../domain/batch";
import { fmt } from "../domain/format";
import { presetModelId } from "../domain/presets";
import { reviewLocation, settleReview, syncReview } from "../domain/review";
import { pick, randInt, reseed } from "../domain/random";
import type {
  BatchFile, BatchMode, BatchPhase, BatchTab, CategoryId, FileFilter, ImageAction, KeepDays, ManifestFilter,
} from "../domain/types";
import { getState, store, type AppState } from "../store";
import {
  closeModal, closePop, openDrawer, openMenu, openModal, openPop, toast, type PopAnchor, type SampleRow,
} from "../overlay/store";
import { needModel } from "./models";
import { newSingle } from "./single";
import { nextId, stopBatchTimer, timers } from "./timers";
import { setContinuation } from "./pending";
import {
  cancelLiveBatch, chooseOutputFolder, flushLiveDecisions, openLiveFile, publishLiveBatch, resumeLiveBatch, runLiveBatch, setLiveOutput,
  stopLiveBatch, toggleLivePause,
} from "./live";

const BATCH_TICK_MS = 240;
const plain = <T,>(v: T): T => (isDraft(v) ? (current(v as Draft<T>) as T) : v);

/* ── Creating, starting and the clock ─────────────────────────────────────── */

export function newBatch(names: string[], opt: { title?: string } = {}): void {
  const s = getState();
  const batch = createBatch(names, { title: opt.title, preset: s.homePreset, modelId: presetModelId(s.presets, s.homePreset) });
  store.update((d) => { d.batch = batch; d.view = "batch"; });
}

export function startBatchClock(): void {
  stopBatchTimer();
  timers.batch = setInterval(tickBatch, BATCH_TICK_MS);
}

export function startBatch(): void {
  const b = getState().batch!;
  if (b.live) { void runLiveBatch(); return; }
  if (!needModel(b.preset, () => startBatch(), b.title + " · " + b.n + " 份", b.modelId)) return;
  store.update((d) => beginRun(d.batch!));
  startBatchClock();
}

/** One step of the simulated clock. */
export function tickBatch(): void {
  const s = getState();
  if (!s.batch || s.batch.phase === "precheck") return;
  let toResult = false;
  store.update((d) => {
    const b = d.batch!;
    advanceFiles(b);
    if (["run", "review"].includes(b.phase) && isSettled(b)) {
      // Keep the review-complete card readable: at least 1.2 s, and 0.8 s after everything has finished.
      const rv = b.rv;
      if (b.phase === "review" && d.view === "batch" && rv && rv.fin) {
        const now = Date.now();
        rv.setAt = rv.setAt || now;
        if (now - rv.finAt! < 1200 || now - rv.setAt < 800) { syncReview(b); settleReview(b); return; }
      }
      b.phase = "result";
      toResult = d.view === "batch";
      return;
    }
    if (d.view === "batch" && b.phase === "review") { syncReview(b); settleReview(b); }
  });
  if (toResult) closePop();
  if (getState().batch?.files.every((f) => ["done", "partial", "bad", "skipped"].includes(f.st))) stopBatchTimer();
}

/* ── Precheck settings ────────────────────────────────────────────────────── */

const guarded = (what: string): boolean => {
  if (!getState().modelPending) return false;
  toast("请先取消等待的操作再修改" + what);
  return true;
};

export function setBatchMode(mode: BatchMode): void {
  if (guarded("设置")) return;
  store.update((d) => { d.batch!.mode = mode; });
}

export function openBatchSchemeMenu(anchor: PopAnchor): void {
  if (guarded("方案")) return;
  const s = getState();
  openMenu(anchor, s.presets.map((p) => ({ l: p.name, s: p.ver, k: p.name, on: s.batch!.preset === p.name })), (it) => {
    store.update((d) => {
      d.batch!.preset = String(it.k);
      d.batch!.modelId = presetModelId(d.presets, String(it.k));
    });
  }, { alignRight: true });
}

export function openBatchImageMenu(anchor: PopAnchor): void {
  const b = getState().batch!;
  openMenu(anchor, [
    { l: "涂抹", k: "remove", on: b.imgAct === "remove" },
    { l: "保留", s: "部分结果", k: "keep", on: b.imgAct === "keep" },
  ], (it) => store.update((d) => { d.batch!.imgAct = it.k as ImageAction; }), { alignRight: true });
}

export function openBatchOutputMenu(anchor: PopAnchor): void {
  const b = getState().batch!;
  if (b.live) {
    openMenu(anchor, [{ l: b.out || "默认位置", k: "cur", on: true }, { l: "选择…", ic: "folder", k: "pick" }], (it) => {
      if (it.k === "pick") void chooseOutputFolder(setLiveOutput);
    }, { alignRight: true });
    return;
  }
  openMenu(anchor, [
    { l: "桌面 / 已脱敏", k: "桌面 / 已脱敏", on: b.out === "桌面 / 已脱敏" },
    { l: "选择…", ic: "folder", k: "文稿 / 脱敏批次" },
  ], (it) => store.update((d) => { d.batch!.out = String(it.k); }), { alignRight: true });
}

export function openKeepMenu(anchor: PopAnchor, current: KeepDays, onPick: (k: KeepDays) => void): void {
  openMenu(anchor, [
    { l: "保留 7 天", k: "7", on: current === "7" },
    { l: "保留 30 天", k: "30", on: current === "30" },
    { l: "不保留", k: "0", on: current === "0" },
  ], (it) => onPick(it.k as KeepDays), { alignRight: true });
}

export function openBatchKeepMenu(anchor: PopAnchor): void {
  openKeepMenu(anchor, getState().batch!.keep, (k) => store.update((d) => { d.batch!.keep = k; }));
}

export function toggleAutoPublish(): void {
  store.update((d) => { d.batch!.autoPub = !d.batch!.autoPub; });
}

export function showUnprocessableFiles(anchor: PopAnchor): void {
  openPop(anchor, { kind: "badFiles" }, { role: "dialog", focus: false });
}

export function cancelBatch(): void {
  if (getState().batch?.live) cancelLiveBatch();
  setContinuation(null);
  store.update((d) => { d.modelPending = null; d.batch = null; d.view = "home"; });
}

/* ── Running ──────────────────────────────────────────────────────────────── */

export function togglePause(): void {
  if (getState().batch?.live) { toggleLivePause(); return; }
  store.update((d) => { d.batch!.paused = !d.batch!.paused; });
  toast(getState().batch!.paused ? "已暂停派发，进行中的会完成" : "已继续");
}

/** Keep the batch for later: it is paused and listed under “最近”. */
export function parkBatch(days: KeepDays): void {
  stopBatchTimer();
  store.update((d) => {
    const b = d.batch!;
    b.paused = true;
    d.history.unshift({ id: nextId("h"), k: "batch", name: b.title, meta: fmt(b.n) + " 份", st: "keep", stl: "已保存 · " + days + " 天后清理", when: "刚刚", saved: b });
    d.batch = null;
  });
}

export function archiveBatch(): void {
  stopBatchTimer();
  store.update((d) => {
    const b = d.batch!;
    const [st, stl] = batchStatus(b);
    d.history.unshift({ id: nextId("h"), k: "batch", name: b.title, meta: fmt(b.n) + " 份", st, stl, when: "刚刚", saved: b });
    d.batch = null;
  });
}

/** Stop the batch, discard its working data and remember how far it got. Callers decide what to show next. */
export function discardBatch(d: Draft<AppState>): void {
  stopBatchTimer();
  const b = d.batch!;
  if (b.phase !== "precheck") {
    const exported = exportedCount(countFiles(b.files));
    d.history.unshift({ id: nextId("h"), k: "batch", name: b.title, meta: fmt(b.n) + " 份", st: "stop", stl: "已停止 · 已导出 " + fmt(exported) + " / " + fmt(b.n), when: "刚刚" });
  }
  d.batch = null;
}

export function openStopMenu(anchor: PopAnchor): void {
  const b = getState().batch!;
  if (b.live) {
    openMenu(anchor, [
      { l: "暂停并保留以便继续", ic: "clock", k: "keep" },
      { l: "停止并清理", ic: "trash", k: "clean", d: true },
    ], (it) => { void stopLiveBatch(it.k === "keep"); }, { alignRight: true });
    return;
  }
  const days = keepDays(b);
  openMenu(anchor, [
    { l: "停止并保留以便恢复", ic: "clock", k: "keep", s: days + " 天" },
    { l: "停止并清理", ic: "trash", k: "clean", d: true },
  ], (it) => {
    if (it.k === "keep") {
      parkBatch(days);
      toast("已保存 " + days + " 天，可在“最近”中继续");
    } else {
      const exported = exportedCount(countFiles(getState().batch!.files));
      store.update((d) => discardBatch(d));
      toast("已停止并清理；已导出的 " + fmt(exported) + " 份不受影响");
    }
    store.update((d) => { d.view = "home"; });
  }, { alignRight: true });
}

export function resumeBatch(entryId: string): void {
  const entry = getState().history.find((h) => h.id === entryId);
  const saved = entry?.saved as import("../domain/types").Batch | undefined;
  if (!entry || !saved) return;
  if (saved.live) {
    store.update((d) => { d.history = d.history.filter((h) => h.id !== entryId); });
    void resumeLiveBatch(saved);
    return;
  }
  if (!needModel(saved.preset, () => resumeBatch(entryId), entry.name, saved.modelId)) return;
  store.update((d) => {
    d.history = d.history.filter((h) => h.id !== entryId);
    d.batch = { ...saved, paused: false };
    d.view = "batch";
  });
  stopBatchTimer();
  if (saved.phase !== "precheck") startBatchClock();
}

export function setBatchPhase(phase: BatchPhase): void {
  closeModal();
  store.update((d) => {
    let v = phase;
    if (v === "run" && machineDone(d.batch!)) v = "result";
    d.batch!.phase = v;
  });
}

export function setBatchTab(tab: BatchTab): void {
  store.update((d) => { d.batch!.tab = tab; });
}

export function setFileFilter(ff: FileFilter): void {
  store.update((d) => { d.batch!.ff = ff; d.batch!.lim = 60; });
}

export function setFileQuery(q: string): void {
  store.update((d) => { d.batch!.q = q.trim(); d.batch!.lim = 60; });
}

export function showMoreFiles(): void {
  store.update((d) => { d.batch!.lim = (d.batch!.lim || 60) + 60; });
}

/* ── Manifest (batch, single file and archived results share one page) ────── */

function manifestHolder(d: Draft<AppState>): { manifestFilter?: ManifestFilter; manifestLimit?: number } {
  if (d.view === "history") {
    const r = d.historyResult!;
    return r.data ?? d.batch!;
  }
  return d.view === "single" ? d.single! : d.batch!;
}

export function setManifestFilter(k: ManifestFilter): void {
  store.update((d) => { const m = manifestHolder(d); m.manifestFilter = k; m.manifestLimit = 80; });
}

export function showMoreManifest(): void {
  store.update((d) => { const m = manifestHolder(d); m.manifestLimit = (m.manifestLimit || 80) + 80; });
}

/* ── Exception groups ─────────────────────────────────────────────────────── */

export function resolveException(gid: string, k: string, only?: number[]): void {
  let res: ReturnType<typeof resolveGroup> = null;
  store.update((d) => { res = resolveGroup(d.batch!, gid, k, only); });
  const r = res as ReturnType<typeof resolveGroup>;
  if (!r) return;
  toast(r.label + " · " + fmt(r.n) + " " + r.unit, () => {
    let ok = false;
    store.update((d) => { ok = undoResolution(d.batch!, r); });
    toast(ok ? "已撤销" : "已导出的文件不能撤销");
  });
}

export function openGroupMenu(anchor: PopAnchor, gid: string): void {
  const b = getState().batch!;
  const g = b.groups.find((x) => x.id === gid)!;
  if (g.noBulk) return;
  const mem = groupMembers(b, g);
  const n = groupTotal(g, mem);
  openMenu(anchor, [
    { h: "整组 · " + fmt(n) + " " + g.unit },
    ...g.acts.map((a) => ({ l: a.k === "pwd" ? "输入密码…" : "全部" + a.l, s: a.k === "pwd" ? "" : fmt(n), k: a.k })),
  ], (it) => {
    if (it.k === "pwd") { openModal({ type: "password", gid, members: undefined }); return; }
    if (it.k === "keep" || it.k === "skip") { openModal({ type: "bulkConfirm", gid, k: String(it.k) }); return; }
    resolveException(gid, String(it.k));
  }, { alignRight: true });
}

export function publishValidated(): void {
  if (getState().batch?.live) { void publishLiveBatch(); return; }
  let n = 0;
  store.update((d) => { n = publishReady(d.batch!); });
  closeModal();
  toast("已导出 " + fmt(n) + " 份");
}

/* ── Sampling ─────────────────────────────────────────────────────────────── */

/** Masked sample values of the demo, per category; a category without any shows a neutral mask. */
const SAMPLE_VALUES: Record<CategoryId | "img", string[]> = {
  organization: ["杭州••••科技有限公司", "上海••••贸易有限公司", "••••律师事务所"],
  person: ["张*桐", "李*轩", "周**", "王*远"],
  phone: ["138 •••• 6624", "0571-•••• 2200", "186 •••• 0931"],
  email: ["li••••@163.com", "hr••••@corp.cn"],
  id_number: ["3301 •••• •••• ••4827", "4403 •••• •••• ••112X"],
  address: ["杭州市西湖区••••", "上海市浦东新区••••"],
  payment_card: ["6222 •••• •••• 885"],
  date: ["1992 年 3 月 ••", "2026 年 1 月 ••"],
  account: ["E10••••27", "E20••••09"],
  ip_address: ["10.12.•••.••", "192.168.•••.••"],
  url: ["https://intra.••••.cn/••••"],
  secret: ["sk-••••••••", "AKIA••••••••"],
  img: ["证件照", "公章", "截图"],
};
let sampleRound = 0;

/** Eight random findings across files, for spot checks. */
export function openSampleDrawer(category: CategoryId | "img", again = false): void {
  const b = getState().batch!;
  reseed(category.length * 31 + (again ? ++sampleRound * 977 : 0));
  const files = b.files.filter((f) => f.det && f.st !== "bad");
  const rows: SampleRow[] = Array.from({ length: 8 }, (_, j) => {
    const f = pick(files);
    const value = pick(SAMPLE_VALUES[category] ?? ["••••••"]);
    const p = randInt(1, f.pages);
    return { fileIndex: f.i, fileName: f.name, location: reviewLocation(f, { p, j }), value };
  });
  openDrawer({ type: "sample", category, rows });
}

/* ── Opening a file of the batch ──────────────────────────────────────────── */

export function openBatchFile(fi: number): void {
  const b = getState().batch!;
  if (b.live) { void openLiveFile(fi); return; }
  const f = b.files[fi]!;
  closeModal();
  if (f.st === "bad") { toast("无法打开 · " + f.reason); return; }
  if (f.st === "skipped") { toast("已跳过 · 不会导出"); return; }
  if (!f.det || f.st === "queue" || f.st === "proc") { toast("尚未检测完成"); return; }
  const size = f.sz || randInt(200000, 6000000);
  store.update((d) => {
    d.batch!.files[fi]!.sz = size;
    if (d.single && d.single.from !== "batch" && !d.behind) d.behind = { single: plain(d.single), undo: plain(d.undo) };
  });
  const saved = f.sv;
  newSingle(f.name, size, { phase: "review", from: "batch", bfi: fi, preset: b.preset, ...(saved ? plain(saved) : {}) });
  if (!saved) {
    store.update((d) => {
      d.single!.imgs.forEach((i) => { i.act = b.imgAct === "keep" ? "keep" : "remove"; });
    });
  }
}

/* ── Leaving a file of the batch ──────────────────────────────────────────── */

/** Edits inside a batch file are written back; an exported file is re-exported and replaces the old version. */
function commitBatchFile(d: Draft<AppState>): "reexp" | "saved" | null {
  const s = d.single;
  if (!s || s.from !== "batch" || !d.batch || !d.undo.length) return null;
  const b = d.batch;
  const f: Draft<BatchFile> = b.files[s.bfi!]!;
  f.sv = { hits: plain(s.hits), pages: plain(s.pages), imgs: plain(s.imgs), catAct: plain(s.catAct) };
  f.man = true;
  if (!["done", "partial", "ready"].includes(f.st)) return "saved";
  const wasExported = f.st !== "ready";
  if (wasExported) f.ver = (f.ver || 1) + 1;
  markBusy(f, "按新决定重新导出");
  if (["result", "manifest"].includes(b.phase)) b.phase = "run";
  return wasExported ? "reexp" : "saved";
}

/** Closes the open batch file (if any), restoring the single task that was parked behind it. */
export function leaveBatchFile(d: Draft<AppState>): "reexp" | "saved" | null {
  const s = d.single;
  if (!s || s.from !== "batch") return null;
  if (s.live) {
    // the choices go to the kernel, which redoes the file; there is no local copy to keep
    const { job } = s.live;
    const findingOf = plain(s.live.findingOf);
    const hits = plain(s.hits);
    const changed = d.undo.length > 0;
    const behind = d.behind;
    d.single = behind ? behind.single : null;
    d.undo = behind ? behind.undo : [];
    d.behind = null;
    if (changed) { queueMicrotask(() => flushLiveDecisions(job, hits, findingOf)); return "saved"; }
    return null;
  }
  const r = commitBatchFile(d);
  const behind = d.behind;
  d.single = behind ? behind.single : null;
  d.undo = behind ? behind.undo : [];
  d.behind = null;
  return r;
}

export function reportLeftBatchFile(result: "reexp" | "saved" | null, where: "page" | "batch"): void {
  if (!result) return;
  // A fully finished batch has stopped its clock; editing an exported file starts work again.
  if (!timers.batch && getState().batch?.phase === "run") startBatchClock();
  toast(result === "reexp" ? (where === "batch" ? "已保存 · 该文件将重新导出并替代旧版本" : "已保存 · 该文件将重新导出") : "已保存修改");
}
