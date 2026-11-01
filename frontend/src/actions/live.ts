// The real processing flow: the screens drive the Go kernel through the Wails service and show what it reports.
// Nothing here simulates progress; every state on screen comes from an engine status or event.
import { isDraft, current } from "immer";
import { isSettled } from "../domain/batch";
import { extOf } from "../domain/format";
import {
  buildLiveDocument, decisionsOf, engineCategory, GROUP_BLOCKED, GROUP_PASSWORD, GROUP_REVIEW, GROUP_STRICT, liveGroups, reasonLabel, toBatchFile,
  unitOffsets, zeroHits,
} from "../domain/live";
import { createSingle } from "../domain/single";
import type { Batch, BatchFile, CategoryId, RasterDoc, RasterPage, Single } from "../domain/types";
import { closeModal, closePop, openModal, overlayStore, toast } from "../overlay/store";
import { getService } from "../services";
import type { EngineBatch, EngineFile, EnginePagePreview, EngineService } from "../services/types";
import type { ManualSelection } from "../domain/single";
import { getState, store } from "../store";
import { slotBusy } from "../store/selectors";
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
  BATCH_ACTIVE: "还有一个未完成的批次，请先处理完或清理",
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
  const s = getState();
  if (paths.length === 1) {
    if (slotBusy(s, "single")) { toast("「" + slotBusy(s, "single")!.name + "」还未导出，请先处理"); return; }
    void startLiveSingle(paths[0]!);
  } else {
    if (slotBusy(s, "batch")) { toast("当前批次还未结束，请先处理"); return; }
    void startLiveBatch(paths);
  }
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
const STOPPED = new Set(["failed", "unprocessable", "cancelled", "skipped"]);

async function startLiveSingle(path: string): Promise<void> {
  const e = liveEngine()!;
  const name = baseName(path);
  const runId = nextRunId();
  store.update((d) => {
    d.single = createSingle(name, extOf(name), undefined, { mode: d.docMode, preset: d.homePreset, modelId: null }, {
      runId, size: "", pages: [], hits: [], imgs: [], live: { batch: "", job: "", findingOf: [], seqOf: [], truncated: false },
    });
    d.undo = [];
    d.view = "single";
  });
  try {
    const out = await e.defaultOutputDir();
    store.update((d) => { if (d.single?.runId === runId) d.single.out = out; });
    await flushConfig(); // the scheme the task runs with must be stored before it is named
    const preset = presetEngineId(getState().homePreset);
    const st = await e.importFiles({ files: [path], quick: true, preset });
    store.update((d) => { if (d.single?.runId === runId) d.single.live!.batch = st.id; });
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
  if (f.state === "locked" || (f.state === "blocked" && (f.detail === "password_required" || f.detail === "PASSWORD_INCORRECT"))) {
    if (!isModalOpen("livePassword")) openModal({ type: "livePassword", targets: [{ job: f.id, name: s.name }] });
    return;
  }
  if (STOPPED.has(f.state) || (f.state === "blocked" && s.phase === "processing")) {
    await abandonSingle(runId, (reasonLabel(f.detail) ?? "文件无法处理") + " · " + s.name);
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
      toast((reasonLabel(f.detail) ?? "导出未完成") + " · 可重试导出");
    } else {
      store.update((d) => { if (d.single?.runId === runId) { d.single.stage = f.state === "validating" || f.state === "publishing" ? 1 : 0; d.single.sp = 50; } });
    }
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
    const finding = await e.addManual(s.live.job, seq, start, end, engineCategory(type));
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
    i, id: "T" + String(i + 1).padStart(4, "0"), name, ext: extOf(name), pages: 0, h: zeroHits(), imgs: 0, st: "queue", t: 0, g: [], late: [],
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
  store.update((d) => {
    const b: Batch = {
      title: names.length + " 份文档", n: names.length, files, groups: [], phase: "precheck", mode: "guided", preset: d.homePreset, modelId: null,
      imgAct: "remove", out: outputDir, autoPub: true, keep: "7", paused: false, tab: "todo", ff: "all", q: "",
      live: { id: "", jobs: [], outputDir },
    };
    d.batch = b;
    d.view = "batch";
  });
  try {
    await flushConfig();
    const st = await e.importFiles({ files: paths, quick: false, outputDir, preset: presetEngineId(getState().homePreset) });
    store.update((d) => { if (d.batch?.live) d.batch.live.id = st.id; });
    await syncBatch(await e.preflight(st.id));
  } catch (err) {
    store.update((d) => { d.batch = null; d.view = "home"; });
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
        d.history.unshift({ id: nextId("h"), k: "batch", name: x.title, meta: x.n + " 份", st: "keep", stl: "已暂停 · 可继续", when: "刚刚", saved: plain(x) });
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

/** Puts back what the last run left unfinished: a quick task continues in its window, a batch is listed
 *  under “最近” and continues when chosen. Nothing runs until the person chooses (the kernel keeps it paused). */
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
    if (b.kind === "quick") {
      if (s.single || !b.files?.length) continue;
      await restoreQuick(b);
    } else if (!s.batch && !s.history.some((h) => h.saved && (h.saved as Batch).live?.id === b.id)) {
      await restoreBatch(b);
    }
  }
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
    await syncSingle(await e.status(b.id, 10));
    toast("已恢复上次未完成的文件");
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
      d.history.unshift({ id: nextId("h"), k: "batch", name: batch.title, meta: b.total + " 份", st: "keep", stl: "已暂停 · 可继续", when: "上次", saved: batch });
    });
  } catch { /* the batch stays in the kernel; the next start tries again */ }
}
