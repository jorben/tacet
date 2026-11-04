// The real processing flow: the screens drive the Go kernel through the Wails service and show what it reports.
// Nothing here simulates progress; every state on screen comes from an engine status or event.
import { isDraft, current, type Draft } from "immer";
import { countFiles, isSettled } from "../domain/batch";
import { extOf } from "../domain/format";
import {
  buildLiveDocument, decisionsOf, GROUP_BLOCKED, GROUP_PASSWORD, GROUP_REVIEW, GROUP_STRICT, liveGroups, passwordWait, reasonLabel, toBatchFile,
  unitOffsets,
} from "../domain/live";
import { createSingle } from "../domain/single";
import type { Batch, BatchFile, CategoryId, HistoryEntry, RasterDoc, RasterPage, Single } from "../domain/types";
import { closeModal, closePop, openModal, overlayStore, toast } from "../overlay/store";
import { getService } from "../services";
import type { EngineBatch, EngineFile, EnginePagePreview, EngineService } from "../services/types";
import type { ManualSelection } from "../domain/single";
import { getState, store, type AppState } from "../store";
import { slotSingle } from "../store/selectors";
import { shelveLiveBatch, shelveSingle } from "./shelf";
import { nextId, nextRunId } from "./timers";
import { flushConfig, loadConfig, presetEngineId, refreshModels } from "./liveConfig";

const plain = <T,>(v: T): T => (isDraft(v) ? (current(v as never) as T) : v);

/** The kernel behind the host, when there is one. */
export const liveEngine = (): EngineService | undefined => getService().engine;
export const isLive = (): boolean => !!liveEngine();

const baseName = (p: string): string => p.split(/[\\/]/).pop() || p;

/** Why a call failed, as a sentence; the kernel only ever sends closed codes. */
const CALL_ERRORS: Record<string, string> = {
  QUICK_TASK_ACTIVE: "还有一份未完成的单文件任务，请先处理完或清理",
  BATCH_ACTIVE: "当前批次还在处理或尚未开始，请等它停下、先暂停，或清理后再试",
  WORKSPACE_BUSY: "工作区正被另一个 Tacet 使用",
  WORKSPACE_KEY_MISSING: "找不到工作区密钥，无法打开已有数据",
  KEYSTORE_UNAVAILABLE: "无法使用系统钥匙串",
  RUNTIME_MISSING: "内置处理组件缺失，请重新安装",
  INVALID_REQUEST: "所选文件或输出位置不可用",
  NOT_READY: "没有可处理的文件",
  OUTPUT_UNAVAILABLE: "无法使用输出位置",
};
export const errorText = (e: unknown): string => {
  const code = String((e as { message?: string })?.message ?? e).replace(/^.*?([A-Z][A-Z0-9_]{2,})\b.*$/s, "$1");
  return CALL_ERRORS[code] ?? reasonLabel(code) ?? "操作未完成（" + code + "）";
};

/* ── Starting and listening ───────────────────────────────────────────────── */

let started = false;

/** Subscribes to the kernel's events; call once when the application mounts. */
export function startLive(): () => void {
  const e = liveEngine();
  if (!e || started) return () => {};
  started = true;
  const offs = [
    e.onBatchChanged((b) => void onBatchChanged(b)),
    e.onFilesDropped((files) => takePaths(files)),
  ];
  void e.info().then((i) => {
    if (!i.ready) toast("处理引擎未能启动：" + errorText(i.problem));
    else void loadConfig().then(restoreUnfinished, restoreUnfinished);
  }).catch(() => {});
  return () => { offs.forEach((o) => o()); started = false; };
}

/** The chooser of the system, then the same path as a drop. */
export async function pickAndTake(): Promise<void> {
  const e = liveEngine();
  if (!e) return;
  try {
    const files = await e.pickFiles();
    if (files.length) takePaths(files);
  } catch (err) { toast(errorText(err)); }
}

/** Absolute paths of the documents the person gave: one is a single task, several are a batch. */
export function takePaths(paths: string[]): void {
  if (!paths.length) return;
  if (paths.length === 1) {
    if (!singleSlotFree()) return;
    void startLiveSingle(paths[0]!);
  } else {
    if (!liveBatchSlotFree()) return;
    void startLiveBatch(paths);
  }
}

/** A single task on its way (being processed or exported) stays on screen; one waiting for the person (to review it, or for its password) gives way. */
export function singleSlotFree(): boolean {
  const cur = slotSingle(getState());
  if (cur && (cur.phase === "exporting" || (cur.phase === "processing" && !cur.live?.locked))) { toast("「" + cur.name + "」正在处理，请稍候"); return false; }
  return true;
}

/**
 * A batch with work under way stays on screen. Only the kernel can tell for one it holds the slot with, and it
 * does at import; a batch that was parked earlier and continued holds none, so the kernel would let the next one
 * in behind it, out of sight of the screen. Files of a paused batch count as being processed, so it is asked first.
 */
export function liveBatchSlotFree(): boolean {
  const b = getState().batch;
  if (!b?.live || b.phase === "precheck" || b.paused) return true;
  const c = countFiles(b.files);
  if (c.queue || c.proc || c.wait || (c.ready && b.autoPub)) { toast("当前批次正在处理，请等它停下或先暂停"); return false; }
  return true;
}

/** Closes a file of the batch that is open: the choices go to the kernel, which redoes the file. There is no local copy to keep. */
export function leaveLiveFile(d: Draft<AppState>): "saved" | null {
  const s = d.single;
  if (!s?.live || s.from !== "batch") return null;
  const { job } = s.live;
  const findingOf = plain(s.live.findingOf);
  const hits = plain(s.hits);
  const changed = d.undo.length > 0;
  const behind = d.behind;
  d.single = behind ? behind.single : null;
  d.undo = behind ? behind.undo : [];
  d.behind = null;
  if (!changed) return null;
  queueMicrotask(() => flushLiveDecisions(job, hits, findingOf));
  return "saved";
}

interface OpenDecisions { job: string; hits: Single["hits"]; findingOf: number[] }

/** The choices on a task under review that the kernel does not have yet: without them there, a restart loses them. */
function openDecisionsOf(s: Single | null | undefined, undo: readonly unknown[]): OpenDecisions | null {
  if (!s?.live || s.phase !== "review" || !(undo.length > 0 || s.live.sent)) return null;
  return { job: s.live.job, hits: plain(s.hits), findingOf: plain(s.live.findingOf) };
}

/** Every choice on screen that only this window holds: the task on screen, and the one behind an open batch file. */
export function openDecisions(s: AppState): OpenDecisions[] {
  return [openDecisionsOf(s.single, s.undo), openDecisionsOf(s.behind?.single, s.behind?.undo ?? [])].filter((x): x is OpenDecisions => !!x);
}

/** Hands choices to the kernel before the window goes away. False, having said so, if the kernel did not take them. */
export async function saveOpenDecisions(open: OpenDecisions[]): Promise<boolean> {
  const e = liveEngine();
  if (!e) return true;
  try {
    await Promise.all(open.map((o) => e.decide(o.job, decisionsOf(o.hits, { findingOf: o.findingOf }))));
    return true;
  } catch (err) {
    toast("你的选择未能保存，窗口未关闭：" + errorText(err));
    return false;
  }
}

/** The single task of the slot leaves the screen for “最近”. A file of the batch that is open is closed first.
 *  What was chosen on it goes to the kernel, which redoes the file: “最近” has to survive a restart. */
function shelveSlotSingle(d: Draft<AppState>): void {
  leaveLiveFile(d);
  const open = openDecisionsOf(d.single as Single | null, d.undo);
  if (open) {
    d.single!.live!.sent = true;
    queueMicrotask(() => flushLiveDecisions(open.job, open.hits, open.findingOf));
  }
  shelveSingle(d);
}

async function onBatchChanged(st: EngineBatch): Promise<void> {
  if (getState().models.some((m) => m.runtime)) void refreshModels();
  const s = getState();
  if (s.single?.live?.batch === st.id) await syncSingle(st);
  else if (s.behind?.single.live?.batch === st.id) { /* a parked single task is read when it comes back */ }
  if (s.batch?.live?.id === st.id) await syncBatch(st);
}

/* ── Single file ──────────────────────────────────────────────────────────── */

const PROGRESS: Record<string, [number, number]> = {
  preflighting: [0, 15], preflighted: [0, 25], queued: [0, 35], extracting_detecting: [0, 65], resolving: [1, 15],
};
/** The kernel ended the file: nothing more can be done with it. Failed and blocked files are not among these. */
const ENDED = new Set(["unprocessable", "cancelled", "skipped"]);

async function startLiveSingle(path: string): Promise<void> {
  const e = liveEngine()!;
  const name = baseName(path);
  const runId = nextRunId();
  let st: EngineBatch;
  let preset: string | undefined;
  let out: string;
  // The screen changes once the kernel took the file: a refusal leaves the task on screen as it is.
  try {
    out = await e.defaultOutputDir();
    await flushConfig(); // the scheme the task runs with must be stored before it is named
    preset = presetEngineId(getState().homePreset);
    st = await e.importFiles({ files: [path], quick: true, preset, parkWaiting: true });
  } catch (err) {
    toast(errorText(err));
    return;
  }
  let shelved = false;
  const leaving = slotSingle(getState())?.live?.job;
  const modal = overlayStore.get().modal;
  // the dialog of a task that leaves the screen would stand in the way of the next one's
  if (modal?.type === "livePassword" && modal.targets.some((t) => t.job === leaving)) closeModal();
  store.update((d) => {
    shelved = !!slotSingle(d) && slotSingle(d)!.phase !== "done";
    shelveSlotSingle(d);
    d.single = createSingle(name, extOf(name), undefined, { mode: d.docMode, preset: d.homePreset, modelId: null }, {
      runId, size: "", out, pages: [], hits: [], imgs: [], live: { batch: st.id, job: "", findingOf: [], seqOf: [], truncated: false },
    });
    d.undo = [];
    d.view = "single";
  });
  if (shelved) toast("上一份文件已保存到“最近”，可稍后继续");
  try {
    await e.start({ batch: st.id, preset, authorize: false, allowPartial: false });
    await syncSingle(await e.status(st.id, 10));
  } catch (err) {
    await abandonSingle(runId, errorText(err));
  }
}

/** The task ended before review: say why, give the quick slot back and leave. */
async function abandonSingle(runId: number, why: string): Promise<void> {
  const s = getState().single;
  if (!s || s.runId !== runId) return;
  const batch = s.live?.batch;
  store.update((d) => { d.single = null; d.view = "home"; });
  toast(why);
  const e = liveEngine();
  if (e && batch) { try { await e.cancel(batch); await e.clean(batch); } catch { /* the slot is freed on the next start */ } }
}

let loading = false;

async function syncSingle(st: EngineBatch): Promise<void> {
  const f = st.files?.[0];
  const s = getState().single;
  if (!f || !s?.live) return;
  const runId = s.runId;
  const locked = passwordWait(f);
  if (locked !== !!s.live.locked) store.update((d) => { if (d.single?.runId === runId) d.single.live!.locked = locked; });
  if (locked) {
    if (!isModalOpen("livePassword")) openModal({ type: "livePassword", targets: [{ job: f.id, name: s.name }] });
    return;
  }
  if (ENDED.has(f.state)) {
    await abandonSingle(runId, (reasonLabel(f.detail) ?? "文件无法处理") + " · " + s.name);
    return;
  }
  if (s.phase === "processing" && (f.state === "failed" || f.state === "blocked")) {
    await reviewStopped(runId, f);
    return;
  }
  if (s.phase === "processing") {
    if (f.state === "awaiting_review" || f.state === "ready_to_publish") {
      if (loading) return;
      loading = true;
      try { await showReview(runId, f.id); } finally { loading = false; }
      return;
    }
    const [stage, sp] = PROGRESS[f.state] ?? [0, 50];
    store.update((d) => { if (d.single?.runId === runId) { d.single.stage = stage; d.single.sp = sp; d.single.live!.job = f.id; } });
    return;
  }
  if (s.phase === "exporting") {
    if (f.state === "completed" || f.state === "partial") finishSingle(runId, f);
    else if (f.state === "awaiting_review") {
      store.update((d) => { if (d.single?.runId === runId) d.single.phase = "review"; });
      toast("还有待确认的内容，请检查后再导出");
    } else if (f.state === "failed" || f.state === "blocked") {
      store.update((d) => { if (d.single?.runId === runId) d.single.phase = "review"; });
      toast(stoppedText(f, "导出未完成"));
    } else {
      store.update((d) => { if (d.single?.runId === runId) { d.single.stage = f.state === "validating" || f.state === "publishing" ? 1 : 0; d.single.sp = 50; } });
    }
  }
}

/** Why a file stopped, and that exporting tries again unless the kernel cannot retry it (the original changed). */
const stoppedText = (f: EngineFile, fallback: string): string =>
  (reasonLabel(f.detail) ?? fallback) + (f.detail === "INPUT_CHANGED" ? "" : " · 可重试导出");

/** A file that stopped as it was opened (found so after a restart, or when coming back to it). The kernel keeps it, and
 *  the decisions with it, so “继续” must not destroy them: its document opens and exporting tries again. Only a file
 *  that stopped before its document was read has nothing to go back to, and the slot is given back. */
async function reviewStopped(runId: number, f: EngineFile): Promise<void> {
  if (loading) return;
  loading = true;
  try {
    const read = await liveEngine()!.documentUnits(f.id, 0, 1).then((units) => units.length > 0, () => false);
    if (!read) { await abandonSingle(runId, (reasonLabel(f.detail) ?? "文件无法处理") + " · " + getState().single?.name); return; }
    await showReview(runId, f.id);
    if (getState().single?.runId === runId) toast(stoppedText(f, "处理未完成"));
  } finally {
    loading = false;
  }
}

const isModalOpen = (type: string): boolean => overlayStore.get().modal?.type === type;

async function loadUnits(job: string) {
  const e = liveEngine()!;
  const all = [];
  let truncated = false;
  for (let after = 0; ; ) {
    const page = await e.documentUnits(job, after, 100);
    if (!page.length) break;
    all.push(...page);
    after = page[page.length - 1]!.seq;
    if (all.length >= 3000) { truncated = true; break; }
  }
  return { units: all, truncated };
}

const rasterPage = (p: EnginePagePreview): RasterPage => ({
  image: p.image, width: p.width, height: p.height,
  boxes: (p.boxes ?? []).map((b) => ({ finding: b.finding, rect: b.rect as RasterPage["boxes"][number]["rect"] })),
  unmapped: (p.unmapped ?? []).length,
});

/** A PDF is shown as the rendered pages. The first page tells how many there are; if it cannot be drawn the
 *  text of the document is shown instead, and the person is told. */
async function openRaster(job: string): Promise<{ doc: RasterDoc; pages: number } | null> {
  try {
    const first = await liveEngine()!.pagePreview(job, 1, RASTER_DPI);
    return { doc: { pages: { 0: rasterPage(first) }, failed: {} }, pages: first.pages };
  } catch {
    toast("页面预览不可用，改为文本显示");
    return null;
  }
}

const RASTER_DPI = 100;
const rasterBusy = new Set<string>();

/** Renders one more page when the person scrolls to it. */
export async function ensureRasterPage(pageIndex: number): Promise<void> {
  const s = getState().single;
  if (!s?.live?.raster || s.live.raster.pages[pageIndex] || s.live.raster.failed[pageIndex]) return;
  const key = s.live.job + ":" + pageIndex;
  if (rasterBusy.has(key)) return;
  rasterBusy.add(key);
  const { job } = s.live;
  const runId = s.runId;
  try {
    const p = rasterPage(await liveEngine()!.pagePreview(job, pageIndex + 1, RASTER_DPI));
    store.update((d) => { if (d.single?.runId === runId && d.single.live?.raster) d.single.live.raster.pages[pageIndex] = p; });
  } catch {
    store.update((d) => { if (d.single?.runId === runId && d.single.live?.raster) d.single.live.raster.failed[pageIndex] = true; });
  } finally {
    rasterBusy.delete(key);
  }
}

/** Pads the pages of the text view up to the page count of the rendered document. */
function padPages(pages: Single["pages"], seqOf: number[][], count: number): void {
  while (pages.length < count) { pages.push([]); seqOf.push([]); }
}

async function showReview(runId: number, job: string): Promise<void> {
  const s = getState().single;
  if (!s || s.runId !== runId) return;
  try {
    const { units, truncated } = await loadUnits(job);
    const doc = buildLiveDocument(units, s.ext);
    const raster = s.ext === "pdf" ? await openRaster(job) : null;
    if (raster) padPages(doc.pages, doc.seqOf, raster.pages);
    store.update((d) => {
      const x = d.single;
      if (!x || x.runId !== runId) return;
      Object.assign(x, { pages: doc.pages, hits: doc.hits, imgs: [], phase: "review", stage: 0, sp: 0 });
      x.live = { ...x.live!, job, findingOf: doc.findingOf, seqOf: doc.seqOf, truncated, raster: raster?.doc };
    });
    if (truncated) toast("文档较长，只显示前一部分；其余内容按方案自动处理");
  } catch (err) {
    await abandonSingle(runId, errorText(err));
  }
}

function finishSingle(runId: number, f: EngineFile): void {
  store.update((d) => {
    const x = d.single;
    if (!x || x.runId !== runId) return;
    x.phase = "done";
    x.live!.output = f.output;
    x.live!.digest = f.digest;
    const partial = f.state === "partial";
    d.history.unshift({
      id: nextId("h"), k: "file", ext: x.ext, name: x.name, meta: x.pages.length + " 页", st: partial ? "part" : "done",
      stl: partial ? "部分结果" : "已导出", when: "刚刚", saved: plain(x),
    });
  });
}

/** Sends every choice to the kernel, authorizes publishing and waits for the events to end the task. */
export async function exportLiveSingle(): Promise<void> {
  const e = liveEngine()!;
  const s = getState().single;
  if (!s?.live) return;
  const { batch, job } = s.live;
  const runId = s.runId;
  const out = s.out;
  closePop();
  store.update((d) => { if (d.single?.runId === runId) { d.single.phase = "exporting"; d.single.stage = 0; d.single.sp = 30; } });
  try {
    const ds = decisionsOf(s.hits, s.live);
    if (ds.length) await e.decide(job, ds);
    await e.publish(batch, false, out);
    await e.retry(batch, [job]); // a file that stopped goes again; one that did not is left as it is
    await syncSingle(await e.status(batch, 10));
  } catch (err) {
    store.update((d) => { if (d.single?.runId === runId) d.single.phase = "review"; });
    toast(errorText(err));
  }
}

/** Stops a single task that is still being processed. */
export function cancelLiveSingle(): void {
  const s = getState().single;
  const batch = s?.live?.batch;
  store.update((d) => { d.single = null; d.view = "home"; });
  toast("已停止");
  const e = liveEngine();
  if (e && batch) void e.cancel(batch).then(() => e.clean(batch)).catch(() => {});
}

/** A selection of the document becomes a redacted candidate in the kernel; the document is then read again. */
export async function addLiveManual(type: CategoryId, sel: ManualSelection): Promise<boolean> {
  const e = liveEngine()!;
  const s = getState().single;
  if (!s?.live) return false;
  const [pi, bi, si] = sel.k.split(".").map(Number) as [number, number, number];
  const block = s.pages[pi]?.[bi];
  const seq = s.live.seqOf[pi]?.[bi];
  if (!block || seq == null) return false;
  const [start, end] = unitOffsets(block, s.hits, si, sel.st, sel.en);
  const runId = s.runId;
  try {
    const finding = await e.addManual(s.live.job, seq, start, end, type);
    const { units, truncated } = await loadUnits(s.live.job);
    store.update((d) => {
      const x = d.single;
      if (!x || x.runId !== runId) return;
      const doc = buildLiveDocument(units, x.ext, { findingOf: plain(x.live!.findingOf), hits: plain(x.hits) });
      Object.assign(x, { pages: doc.pages, hits: doc.hits });
      x.live = { ...x.live!, findingOf: doc.findingOf, seqOf: doc.seqOf, truncated };
      // The document was read again, so earlier snapshots no longer match its hits; what can be undone
      // from here on is this selection, which the kernel takes back (FR-22).
      d.undo = [{ hits: plain(x.hits), pages: plain(x.pages), imgs: plain(x.imgs), catAct: plain(x.catAct), manual: finding }];
    });
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** Undo of a manual redaction: the kernel takes the selection back and the document is read again, keeping
 *  the choices made on the other candidates. The undo entry goes only once the kernel agreed. */
export async function withdrawLiveManual(finding: number): Promise<boolean> {
  const e = liveEngine();
  const s = getState().single;
  if (!e || !s?.live) return false;
  const runId = s.runId;
  try {
    await e.withdrawManual(s.live.job, finding);
    const { units, truncated } = await loadUnits(s.live.job);
    store.update((d) => {
      const x = d.single;
      if (!x || x.runId !== runId) return;
      const doc = buildLiveDocument(units, x.ext, { findingOf: plain(x.live!.findingOf), hits: plain(x.hits) });
      Object.assign(x, { pages: doc.pages, hits: doc.hits });
      x.live = { ...x.live!, findingOf: doc.findingOf, seqOf: doc.seqOf, truncated };
      d.undo = [];
    });
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** Decisions of a file opened from a batch are written to the kernel when the person leaves it. */
export function flushLiveDecisions(job: string, hits: Single["hits"], findingOf: number[]): void {
  const e = liveEngine();
  if (!e) return;
  const ds = decisionsOf(hits, { findingOf });
  if (ds.length) void e.decide(job, ds).catch((err) => toast(errorText(err)));
}

/** “跳过此文件”: the file is left out of the batch and not exported. False when the kernel refused. */
export async function skipLive(job: string): Promise<boolean> {
  const e = liveEngine();
  if (!e) return false;
  try {
    await e.skip(job);
    toast("已跳过 · 不会导出");
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** Strict mode, from the file's review: skips this file instead of confirming it. */
export async function skipLiveBatchFile(): Promise<void> {
  const s = getState().single;
  if (!s?.live || s.from !== "batch") return;
  const { job } = s.live;
  closePop();
  store.update((d) => {
    const behind = d.behind;
    d.single = behind ? behind.single : null;
    d.undo = behind ? behind.undo : [];
    d.behind = null;
    d.view = "batch";
  });
  await skipLive(job);
}

/** Strict mode, from the file's review: sends the choices, then confirms this one file for export. */
export async function confirmLiveBatchFile(): Promise<void> {
  const e = liveEngine();
  const s = getState().single;
  if (!e || !s?.live || s.from !== "batch") return;
  const { job } = s.live;
  const ds = decisionsOf(s.hits, s.live);
  closePop();
  store.update((d) => {
    const behind = d.behind;
    d.single = behind ? behind.single : null;
    d.undo = behind ? behind.undo : [];
    d.behind = null;
    d.view = "batch";
  });
  try {
    if (ds.length) await e.decide(job, ds);
    await e.confirm(job);
    toast("已确认 · 该文件将导出");
  } catch (err) { toast(errorText(err)); }
}

export async function chooseOutputFolder(apply: (dir: string) => void): Promise<void> {
  const e = liveEngine();
  if (!e) return;
  try {
    const dir = await e.pickFolder();
    if (dir) apply(dir);
  } catch (err) { toast(errorText(err)); }
}

/* ── Passwords ────────────────────────────────────────────────────────────── */

/** Hands one password to the kernel. It is not kept anywhere on this side. */
export async function unlockLive(job: string, password: string): Promise<boolean> {
  const e = liveEngine();
  if (!e || !password) return false;
  try {
    await e.unlock(job, password);
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/* ── Batch ────────────────────────────────────────────────────────────────── */

const PAGE = 200;

function placeholderFile(i: number, name: string): BatchFile {
  return {
    i, id: "T" + String(i + 1).padStart(4, "0"), name, ext: extOf(name), pages: 0, h: {}, imgs: 0, st: "queue", t: 0, g: [], late: [],
    det: false, partial: false, man: false,
  };
}

async function loadRows(batch: string, total: number, first?: EngineFile[]): Promise<EngineFile[]> {
  const e = liveEngine()!;
  if (first && first.length >= total) return first.slice(0, total);
  const rows: EngineFile[] = [];
  while (rows.length < total) {
    const page = await e.files(batch, rows.length, PAGE);
    if (!page.length) break;
    rows.push(...page);
  }
  return rows;
}

async function startLiveBatch(paths: string[]): Promise<void> {
  const e = liveEngine()!;
  const names = paths.map(baseName);
  let outputDir = "";
  try { outputDir = await e.defaultOutputDir(); } catch { /* chosen later */ }
  const files = names.map((n, i) => placeholderFile(i, n));
  let st: EngineBatch;
  // The screen changes once the kernel took the files: a refusal leaves the batch on screen as it is.
  try {
    await flushConfig();
    st = await e.importFiles({ files: paths, quick: false, outputDir, preset: presetEngineId(getState().homePreset), parkWaiting: true });
  } catch (err) {
    toast(errorText(err));
    return;
  }
  let shelved = false;
  // A draft on screen that the kernel let the import past holds no slot: all its files were refused, nothing is left of it to continue.
  const refused = getState().batch?.phase === "precheck" ? getState().batch?.live?.id : undefined;
  if (refused) void e.clean(refused).catch(() => {});
  store.update((d) => {
    leaveLiveFile(d); // a file of the batch that leaves the screen must not stay open
    shelved = !!d.batch && !refused;
    if (refused) d.batch = null;
    else shelveLiveBatch(d);
    const b: Batch = {
      title: names.length + " 份文档", n: names.length, files, groups: [], phase: "precheck", mode: "guided", preset: d.homePreset, modelId: null,
      imgAct: "remove", out: outputDir, autoPub: true, keep: "7", paused: false, tab: "todo", ff: "all", q: "",
      live: { id: st.id, jobs: [], outputDir },
    };
    d.batch = b;
    d.view = "batch";
  });
  if (shelved) toast("上一个批次已保存到“最近”，可稍后继续");
  try {
    await syncBatch(await e.preflight(st.id));
  } catch (err) {
    store.update((d) => { if (d.batch?.live?.id === st.id) { d.batch = null; d.view = "home"; } });
    toast(errorText(err));
  }
}

let syncing = false;
let again: EngineBatch | null = null;

/** Takes the kernel's picture of the batch into the rows, the groups and the phase. */
async function syncBatch(st: EngineBatch): Promise<void> {
  if (syncing) { again = st; return; }
  syncing = true;
  try {
    let next: EngineBatch | null = st;
    while (next) {
      const cur: EngineBatch = next;
      again = null;
      const rows = await loadRows(cur.id, cur.total, cur.files ?? undefined);
      store.update((d) => {
        const b = d.batch;
        if (!b?.live || b.live.id !== cur.id) return;
        const begun = b.phase !== "precheck";
        b.files = rows.map((r, i) => toBatchFile(b.files[i] ?? placeholderFile(i, r.name), r));
        b.live.jobs = rows.map((r) => r.id);
        b.groups = liveGroups(b.files, rows);
        b.n = cur.total;
        if (begun && (b.phase === "run" || b.phase === "result")) b.phase = isSettled(b) ? "result" : "run";
        if (!begun) b.files.forEach((f) => { if (f.st === "held" || f.st === "proc") f.st = f.st === "proc" ? "queue" : f.st; });
      });
      next = again;
    }
  } finally {
    syncing = false;
  }
}

/** “开始”: freezes the settings in the kernel and lets it run. */
export async function runLiveBatch(): Promise<void> {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) { toast("文件还在检查，请稍候"); return; }
  const id = b.live.id;
  if (!b.files.some((f) => f.st !== "bad")) { toast("没有可处理的文件"); return; }
  store.update((d) => { d.batch!.phase = "run"; d.batch!.tab = "todo"; });
  try {
    await flushConfig();
    await e.start({ batch: id, preset: presetEngineId(b.preset), outputDir: b.live.outputDir, authorize: b.autoPub, allowPartial: b.imgAct === "keep", mode: b.mode === "strict" ? "strict" : undefined });
    await syncBatch(await e.status(id, PAGE));
  } catch (err) {
    store.update((d) => { if (d.batch?.live?.id === id) d.batch.phase = "precheck"; });
    toast(errorText(err));
  }
}

export function setLiveOutput(dir: string): void {
  store.update((d) => {
    if (d.batch?.live) { d.batch.live.outputDir = dir; d.batch.out = dir; }
  });
}

export async function publishLiveBatch(): Promise<void> {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) return;
  closeModal();
  try {
    await e.publish(b.live.id, b.imgAct === "keep", b.live.outputDir);
    toast("已开始导出");
  } catch (err) { toast(errorText(err)); }
}

export function toggleLivePause(): void {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) return;
  const pause = !b.paused;
  store.update((d) => { d.batch!.paused = pause; });
  (pause ? e.pause(b.live.id) : e.resume(b.live.id)).then(
    () => toast(pause ? "已暂停，进行中的步骤会回到起点" : "已继续"),
    (err) => { store.update((d) => { if (d.batch) d.batch.paused = !pause; }); toast(errorText(err)); },
  );
}

/** Stops the batch. Keeping it leaves the kernel's data for a later resume; cleaning removes everything. */
export async function stopLiveBatch(keep: boolean): Promise<void> {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) return;
  const id = b.live.id;
  try {
    if (keep) {
      await e.pause(id);
      store.update((d) => {
        const x = d.batch!;
        x.paused = true;
        d.history.unshift({ id: nextId("h"), k: "batch", name: x.title, meta: x.n + " 份", st: "keep", stl: "已暂停 · 可继续", when: "刚刚", saved: plain(x), park: true });
        d.batch = null;
        d.view = "home";
      });
      toast("已暂停，可在“最近”中继续");
    } else {
      await e.cancel(id);
      await cleanWhenStopped(id);
      store.update((d) => { d.batch = null; d.view = "home"; });
      toast("已停止并清理；已导出的文件不受影响");
    }
  } catch (err) { toast(errorText(err)); }
}

/** Removes a stopped batch's data; the kernel refuses until every file has come to rest, so it is asked again. */
async function cleanWhenStopped(id: string): Promise<void> {
  const e = liveEngine()!;
  let last: unknown;
  for (let i = 0; i < 100; i++) {
    try { await e.clean(id); return; } catch (err) { last = err; await new Promise((r) => setTimeout(r, 100)); }
  }
  throw last;
}

/** Brings a kept live batch back and lets the kernel carry on. */
export async function resumeLiveBatch(saved: Batch): Promise<void> {
  const e = liveEngine()!;
  const id = saved.live!.id;
  store.update((d) => { d.batch = { ...saved, paused: false }; d.view = "batch"; });
  try {
    await e.resume(id);
    await syncBatch(await e.status(id, PAGE));
  } catch (err) { toast(errorText(err)); }
}

/** Brings a parked single task back: the choices made on it are still here, what the kernel says is read again.
 *  Pages rendered earlier may have left the kernel's bounded cache, so they are asked for again. */
export async function reopenLiveSingle(entryId: string): Promise<void> {
  const e = liveEngine()!;
  const entry = getState().history.find((h) => h.id === entryId);
  const saved = entry?.saved as Single | undefined;
  if (!entry || !saved?.live || !singleSlotFree()) return;
  store.update((d) => {
    shelveSlotSingle(d);
    d.history = d.history.filter((h) => h.id !== entryId);
    d.single = { ...saved, live: { ...saved.live!, raster: saved.live!.raster ? { pages: {}, failed: {} } : undefined } };
    d.undo = entry.undo ?? [];
    d.view = "single";
  });
  try {
    if (!saved.out) { // listed after a restart: nothing was chosen yet
      const out = await e.defaultOutputDir();
      store.update((d) => { if (d.single?.runId === saved.runId && !d.single.out) d.single.out = out; });
    }
    await e.resume(saved.live.batch);
    await syncSingle(await e.status(saved.live.batch, 10));
  } catch (err) { toast(errorText(err)); }
}

/** Stops a task the kernel keeps and removes its data, which also gives up any claim on its slot. False if it refused. */
export async function discardLiveTask(batch: string): Promise<boolean> {
  const e = liveEngine();
  if (!e) return false;
  try {
    await e.cancel(batch);
    await cleanWhenStopped(batch);
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** Gives up a batch that has not started. */
export function cancelLiveBatch(): void {
  const e = liveEngine();
  const id = getState().batch?.live?.id;
  if (e && id) void e.cancel(id).then(() => cleanWhenStopped(id)).catch(() => {});
}

export async function retryLive(fi?: number): Promise<void> {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) return;
  try {
    const only = fi == null ? [] : [b.live.jobs[fi]!];
    await e.retry(b.live.id, only);
  } catch (err) { toast(errorText(err)); }
}

/** Opens the document of one file of the batch for review, or the dialog it is waiting for. */
export async function openLiveFile(fi: number): Promise<void> {
  const s = getState();
  const b = s.batch!;
  const f = b.files[fi]!;
  const job = b.live!.jobs[fi]!;
  closeModal();
  if (f.g.includes(GROUP_PASSWORD)) { openModal({ type: "livePassword", targets: [{ job, name: f.name }] }); return; }
  if (f.g.includes(GROUP_BLOCKED) || f.st === "bad") { toast((f.reason ?? "无法处理") + " · " + f.name); return; }
  if (f.st === "skipped") { toast("已跳过 · 不会导出"); return; }
  if (f.st === "done" || f.st === "partial") { toast("已导出为 " + (f.outputName ?? f.id)); return; }
  if (!(f.g.includes(GROUP_REVIEW) || f.g.includes(GROUP_STRICT) || f.st === "ready")) { toast("尚未检测完成"); return; }
  const runId = nextRunId();
  try {
    const { units, truncated } = await loadUnits(job);
    const doc = buildLiveDocument(units, f.ext);
    const raster = f.ext === "pdf" ? await openRaster(job) : null;
    if (raster) padPages(doc.pages, doc.seqOf, raster.pages);
    store.update((d) => {
      if (d.single && d.single.from !== "batch" && !d.behind) d.behind = { single: plain(d.single), undo: plain(d.undo) };
      d.single = createSingle(f.name, f.ext, undefined, { mode: d.docMode, preset: d.batch!.preset, modelId: null }, {
        phase: "review", from: "batch", bfi: fi, runId, size: "", out: d.batch!.out, pages: doc.pages, hits: doc.hits, imgs: [],
        live: { batch: d.batch!.live!.id, job, findingOf: doc.findingOf, seqOf: doc.seqOf, truncated, raster: raster?.doc },
      });
      d.undo = [];
      d.view = "single";
    });
    if (truncated) toast("文档较长，只显示前一部分；其余内容按方案自动处理");
  } catch (err) { toast(errorText(err)); }
}

/** A group's row was clicked: open its first file, ask for its passwords, or retry it. */
export function openLiveGroup(gid: string | null): void {
  const b = getState().batch!;
  const g = gid ? b.groups.find((x) => x.id === gid) : b.groups.find((x) => Object.keys(x.per).length);
  if (!g) return;
  const members = Object.keys(g.per).map(Number);
  if (g.id === GROUP_PASSWORD) {
    openModal({ type: "livePassword", targets: members.map((fi) => ({ job: b.live!.jobs[fi]!, name: b.files[fi]!.name })) });
  } else if (g.id === GROUP_BLOCKED) {
    // a whole-file block offers its one unblocking action (重新处理) and “跳过此文件” (requirements 2.3)
    openModal({ type: "liveBlocked", targets: members.map((fi) => ({ job: b.live!.jobs[fi]!, name: b.files[fi]!.name, reason: b.files[fi]!.reason ?? "无法处理" })) });
  } else {
    void openLiveFile(members[0]!);
  }
}

export const liveIdle = (): boolean => !!(getState().batch?.live);

export { GROUP_BLOCKED, GROUP_PASSWORD, GROUP_REVIEW, GROUP_STRICT };

/* ── After a restart ──────────────────────────────────────────────────────── */

/** Puts back what the last run left unfinished: the quick task that holds the slot continues in its window;
 *  parked quick tasks and every batch are listed under “最近” and continue when chosen. Nothing else runs until
 *  the person chooses (the kernel keeps it paused). */
let restoring: Promise<void> | null = null;
export function restoreUnfinished(): Promise<void> {
  // one pass at a time: two overlapping passes would each find the batch not yet listed and list it twice
  restoring = (restoring ?? Promise.resolve()).then(restoreOnce, restoreOnce);
  return restoring;
}

async function restoreOnce(): Promise<void> {
  const e = liveEngine();
  if (!e) return;
  let list: EngineBatch[];
  try { list = await e.unfinished(); } catch { return; }
  for (const b of list) {
    const s = getState();
    if (tracked(s, b.id)) continue;
    if (b.kind === "quick") {
      if (!b.files?.length) continue;
      if (b.parked || s.single) listQuick(b);
      else await restoreQuick(b);
    } else {
      await restoreBatch(b);
    }
  }
}

/** The kernel's batch of a saved task, single or batch. */
export function savedBatchId(saved: HistoryEntry["saved"]): string | undefined {
  const live = (saved as { live?: { id?: string; batch?: string } } | undefined)?.live;
  return live?.id ?? live?.batch;
}

/** The task is on screen, behind an open file of a batch, or listed already. */
function tracked(s: AppState, id: string): boolean {
  return s.batch?.live?.id === id || s.single?.live?.batch === id || s.behind?.single.live?.batch === id ||
    s.history.some((h) => savedBatchId(h.saved) === id);
}

/** A quick task that continues when chosen. After a restart only its neutral name is known, so the batch number
 *  tells two of them apart. The document is read when it is opened. */
function listQuick(b: EngineBatch): void {
  const f = b.files![0]!;
  const name = f.name + "." + f.format;
  store.update((d) => {
    const saved = createSingle(name, f.format, undefined, { mode: d.docMode, preset: d.homePreset, modelId: null }, {
      runId: nextRunId(), size: "", out: "", pages: [], hits: [], imgs: [], live: { batch: b.id, job: f.id, findingOf: [], seqOf: [], truncated: false },
    });
    d.history.unshift({ id: nextId("h"), k: "file", ext: f.format, name, meta: b.id, st: "warn", stl: "待确认", when: "上次", saved, undo: [], park: true });
  });
}

async function restoreQuick(b: EngineBatch): Promise<void> {
  const e = liveEngine()!;
  const f = b.files![0]!;
  const runId = nextRunId();
  const name = f.name + "." + f.format;
  store.update((d) => {
    d.single = createSingle(name, f.format, undefined, { mode: d.docMode, preset: d.homePreset, modelId: null }, {
      runId, size: "", pages: [], hits: [], imgs: [], live: { batch: b.id, job: f.id, findingOf: [], seqOf: [], truncated: false },
    });
    d.undo = [];
    d.view = "single";
  });
  try {
    store.update((d) => { if (d.single?.runId === runId) d.single.out = b.files![0]!.output ?? ""; });
    const out = await e.defaultOutputDir();
    store.update((d) => { if (d.single?.runId === runId && !d.single.out) d.single.out = out; });
    await e.resume(b.id);
    toast("已恢复上次未完成的文件"); // first: what the file reports next is what the person should read
    await syncSingle(await e.status(b.id, 10));
  } catch (err) {
    await abandonSingle(runId, errorText(err));
  }
}

async function restoreBatch(b: EngineBatch): Promise<void> {
  const e = liveEngine()!;
  try {
    const rows = await loadRows(b.id, b.total, b.files ?? undefined);
    const outputDir = await e.defaultOutputDir().catch(() => "");
    const files = rows.map((r, i) => toBatchFile(placeholderFile(i, r.name + "." + r.format), r));
    const batch: Batch = {
      title: b.total + " 份文档", n: b.total, files, groups: liveGroups(files, rows), phase: b.started ? "run" : "precheck", mode: "guided",
      preset: getState().homePreset, modelId: null, imgAct: "remove", out: outputDir, autoPub: !!b.authorized, keep: "7", paused: true,
      tab: "todo", ff: "all", q: "", live: { id: b.id, jobs: rows.map((r) => r.id), outputDir },
    };
    store.update((d) => {
      if (d.history.some((h) => h.saved && (h.saved as Batch).live?.id === b.id)) return; // already listed
      d.history.unshift({ id: nextId("h"), k: "batch", name: batch.title, meta: b.total + " 份", st: "keep", stl: "已暂停 · 可继续", when: "上次", saved: batch, park: true });
    });
  } catch { /* the batch stays in the kernel; the next start tries again */ }
}
