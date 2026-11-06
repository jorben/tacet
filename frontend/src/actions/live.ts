// The real processing flow: the screens drive the Go kernel through the Wails service and show what it reports.
// Nothing here simulates progress; every state on screen comes from an engine status or event.
import { isDraft, current, type Draft } from "immer";
import { isSettled } from "../domain/batch";
import { extOf, fmt } from "../domain/format";
import {
  buildLiveDocument, decisionsOf, GROUP_BLOCKED, GROUP_PASSWORD, GROUP_REVIEW, GROUP_STRICT, liveGroups, passwordWait, reasonLabel, toBatchFile,
  unitOffsets, withDecisions,
} from "../domain/live";
import { createSingle } from "../domain/single";
import type { Batch, BatchFile, CategoryId, HistoryEntry, RasterDoc, RasterPage, Single } from "../domain/types";
import { closeModal, closePop, openModal, overlayStore, toast, type UnsavedTarget } from "../overlay/store";
import { getService } from "../services";
import type { EngineBatch, EngineFile, EnginePagePreview, EngineService } from "../services/types";
import type { ManualSelection } from "../domain/single";
import { getState, store, type AppState } from "../store";
import { isWorking, keptTasks, refusedFiles, screenSingle, type RefusedFile } from "../store/selectors";
import { allDecisionsSettled, decisionsSettled, forgetDecisions, refusalOf, refusedJobs, resendDecisions, sendDecisions, unsavedDecisions, type UnsavedDecisions } from "./decisions";
import { batchEntryStatus, shelveLiveBatch, shelveSingle, singleEntryStatus } from "./shelf";
import { nextId, nextRunId } from "./timers";
import { flushConfig, loadConfig, presetEngineId, refreshModels } from "./liveConfig";

const plain = <T,>(v: T): T => (isDraft(v) ? (current(v as never) as T) : v);

/** The kernel behind the host, when there is one. */
export const liveEngine = (): EngineService | undefined => getService().engine;
export const isLive = (): boolean => !!liveEngine();

const baseName = (p: string): string => p.split(/[\\/]/).pop() || p;

/** Why a call failed, as a sentence; the kernel only ever sends closed codes. */
const CALL_ERRORS: Record<string, string> = {
  DECISIONS_CLOSED: "这份文件已开始导出或已导出，这次修改不会生效",
  BATCH_HAS_OPEN_FILES: "任务还有未结束的文件，暂时无法清理，请稍后再试",
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
  if (paths.length === 1) void startLiveSingle(paths[0]!);
  else void startLiveBatch(paths);
}

/** What leaving the screen means for the task that leaves, as the person is told. */
const leftBehind = (what: string, working: boolean): string =>
  working ? what + "继续在后台处理，可在“最近”查看" : what + "已保存到“最近”，可稍后继续";

/** Closes a file of the batch that is open: the choices go to the kernel, which redoes the file. There is no local copy
 *  to keep. What comes back, if there were choices to send, tells whether the kernel took them. */
export function leaveLiveFile(d: Draft<AppState>): Promise<boolean> | null {
  const s = d.single;
  if (!s?.live || s.from !== "batch") return null;
  const { job } = s.live;
  const { name } = s;
  const findingOf = plain(s.live.findingOf);
  const hits = plain(s.hits);
  const changed = d.undo.length > 0 || !!s.live.sent;
  bringBackBehind(d);
  if (!changed) return null;
  return flushLiveDecisions(name, job, hits, findingOf);
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

/** The files of every task still kept for the person: a refused write on any other has nothing left to save. */
function keptJobs(s: AppState): Set<string> {
  return new Set(keptTasks(s).flatMap((t) => ("files" in t ? t.live!.jobs : [t.live!.job])));
}

/** The refused files, as the person is told how far “停止并清理” reaches. */
const refusedScope = (files: RefusedFile[]): string =>
  "「" + files[0]!.name + "」" + (files.length > 1 ? "等 " + files.length + " 个文件" : "");

/** Hands choices to the kernel before the window goes away. False, having said so, if the kernel did not take them. */
export async function saveOpenDecisions(open: OpenDecisions[]): Promise<boolean> {
  const inKept = (list: UnsavedDecisions[]) => { const kept = keptJobs(getState()); return list.filter((u) => kept.has(u.job)); };
  // The choices on screen go now, not after the wait: one made during it has to land after them.
  open.forEach((o) => { void sendDecisions(o.job, decisionsOf(o.hits, { findingOf: o.findingOf })).catch(() => {}); });
  // Only this window holds what was refused, so it goes again, now that nothing newer can be ahead of it. A file on
  // screen has just been sent: its failure is what the person is told below.
  const onScreen = new Set(open.map((o) => o.job));
  resendDecisions(inKept(await allDecisionsSettled()).map((u) => u.job).filter((job) => !onScreen.has(job)));
  const [refused] = inKept(await allDecisionsSettled());
  if (!refused) return true;
  const files = refusedFiles(getState(), refusedJobs());
  toast("你的选择未能保存，窗口未关闭：" + errorText(refused.failure) + " · 可再试；“停止并清理”也能退出，只清理" + refusedScope(files) + "，其他任务保留，已导出的文件不受影响");
  return false;
}

/** The files of a batch among those whose choices the kernel has not taken. */
const unsavedTargets = (b: Batch, list: UnsavedDecisions[]): UnsavedTarget[] => list.flatMap((u) => {
  const fi = b.live!.jobs.indexOf(u.job);
  return fi < 0 ? [] : [{ job: u.job, name: b.files[fi]?.name ?? u.job, reason: errorText(u.failure) }];
});

/** Before acting on what a batch holds: the person's last choices must be there, not only the writes over. False if
 *  they are not: the dialog names the files and offers what can be done, because a message would be gone before it is read. */
async function decisionsLanded(b: Batch): Promise<boolean> {
  const targets = unsavedTargets(b, await allDecisionsSettled());
  if (!targets.length) return true;
  openModal({ type: "unsavedDecisions", targets });
  return false;
}

/** Sends the refused choices of these files again; those the kernel still did not take, with the reason now. */
export async function resendUnsaved(targets: UnsavedTarget[]): Promise<UnsavedTarget[]> {
  await allDecisionsSettled(); // a write on its way is newer than what was refused: this must not land over it
  resendDecisions(targets.map((t) => t.job));
  await allDecisionsSettled();
  return targets.flatMap((t) => {
    const failure = refusalOf(t.job);
    return failure === undefined ? [] : [{ ...t, reason: errorText(failure) }];
  });
}

/** The single task behind an open file of a batch comes back on screen. It heard nothing meanwhile, so it reads the kernel. */
function bringBackBehind(d: Draft<AppState>): void {
  const behind = d.behind;
  d.single = behind ? behind.single : null;
  d.undo = behind ? behind.undo : [];
  d.behind = null;
  if (behind?.single.live && behind.single.phase !== "done") rereadSingle(behind.single.live.batch);
}

/** Asks the kernel about a task whose last event may have gone to a screen it has left. A failed read changes nothing: the next event tells. */
function rereadSingle(batch: string): void {
  const e = liveEngine();
  if (e) void e.status(batch, 10).then(syncSingle).catch(() => {});
}

/** The single task on screen leaves for “最近”, and goes on if it is under way. A file of the batch that is open is
 *  closed first. What was chosen on it goes to the kernel, which redoes the file: “最近” has to survive a restart. */
export function shelveScreenSingle(d: Draft<AppState>): void {
  leaveLiveFile(d);
  const open = openDecisionsOf(d.single as Single | null, d.undo);
  if (open) {
    d.single!.live!.sent = true;
    flushLiveDecisions(d.single!.name, open.job, open.hits, open.findingOf);
  }
  const s = d.single;
  const reading = s?.phase === "processing" && !s.live?.locked ? s.live?.batch : undefined;
  shelveSingle(d);
  // the document it was reading for an event that came just before is dropped with the screen: the entry has to hear it again
  if (reading) rereadSingle(reading);
}

/** The batch on screen gives way. A draft with every file refused has nothing to continue, so it goes, with its data;
 *  any other goes to “最近” and carries on there. Whether something was kept is returned. */
export function setAsideBatch(d: Draft<AppState>): boolean {
  const b = d.batch;
  if (!b) return false;
  if (b.live && b.phase === "precheck" && b.files.length > 0 && b.files.every((f) => f.st === "bad")) {
    void liveEngine()?.clean(b.live.id).catch(() => {});
    d.batch = null;
    return false;
  }
  shelveLiveBatch(d);
  return true;
}

async function onBatchChanged(st: EngineBatch): Promise<void> {
  if (getState().models.some((m) => m.runtime)) void refreshModels();
  const s = getState();
  const listed = shelvedEntry(s, st.id);
  if (s.single?.live?.batch === st.id) await syncSingle(st);
  else if (listed?.k === "file") await syncShelvedSingle(st);
  if (s.batch?.live?.id === st.id || listed?.k === "batch") await syncBatch(st);
}

/** The entry under “最近” of a task that can still change. */
function shelvedEntry<S extends Pick<AppState, "history">>(s: S, id: string): S["history"][number] | undefined {
  return s.history.find((h) => h.park && savedBatchId(h.saved) === id) as S["history"][number] | undefined;
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
    st = await e.importFiles({ files: [path], quick: true, preset });
  } catch (err) {
    toast(errorText(err));
    return;
  }
  const leavingTask = screenSingle(getState());
  const shelved = !!leavingTask && leavingTask.phase !== "done";
  const working = shelved && isWorking(leavingTask);
  const leaving = leavingTask?.live?.job;
  const modal = overlayStore.get().modal;
  // the dialog of a task that leaves the screen would stand in the way of the next one's
  if (modal?.type === "livePassword" && modal.targets.some((t) => t.job === leaving)) closeModal();
  store.update((d) => {
    shelveScreenSingle(d);
    d.single = createSingle(name, extOf(name), undefined, { mode: d.docMode, preset: d.homePreset, modelId: null }, {
      runId, size: "", out, pages: [], hits: [], imgs: [], live: { batch: st.id, job: "", findingOf: [], seqOf: [], truncated: false },
    });
    d.undo = [];
    d.view = "single";
  });
  if (shelved) toast(leftBehind("上一份文件", working));
  try {
    await e.start({ batch: st.id, preset, authorize: false, allowPartial: false });
    await syncSingle(await e.status(st.id, 10));
  } catch (err) {
    await abandonSingle(runId, errorText(err));
  }
}

/** Reading the task failed, which does not say it ended: the kernel keeps it and its choices. It waits under “最近”,
 *  and continuing reads it again. */
function shelveUnread(runId: number, err: unknown): void {
  if (getState().single?.runId !== runId) return;
  store.update((d) => { shelveSingle(d); d.view = "home"; });
  toast(errorText(err) + " · 已保存到“最近”，可稍后继续");
}

/** The entry under “最近” of a single task, by its run: the batch it belongs to may not be known yet. */
const shelvedRun = (d: Pick<AppState, "history">, runId: number) =>
  d.history.find((h) => h.k === "file" && h.park && (h.saved as Single | undefined)?.runId === runId);

/** The task ended before review: say why and leave it behind, wherever it is by now. */
async function abandonSingle(runId: number, why: string): Promise<void> {
  let batch: string | undefined;
  let found = false;
  store.update((d) => {
    if (d.single?.runId === runId) {
      batch = d.single.live?.batch;
      found = true;
      d.single = null;
      d.undo = [];
      d.view = "home";
    } else if (d.behind?.single.runId === runId) {
      batch = d.behind.single.live?.batch;
      found = true;
      d.behind = null;
    } else {
      const h = shelvedRun(d, runId);
      if (!h) return;
      batch = (h.saved as Single).live?.batch;
      found = true;
      d.history = d.history.filter((x) => x.id !== h.id);
    }
  });
  if (!found) return;
  toast(why);
  const e = liveEngine();
  if (e && batch) { try { await e.cancel(batch); await cleanWhenStopped(batch); } catch { /* it stays in the kernel, which lists it at the next start */ } }
}

/** The runs reading their document right now: another run has a document of its own to read. */
const loading = new Set<number>();

/** Takes what the kernel says about a task under “最近”: it goes on working there, and its entry says how far it is. */
async function syncShelvedSingle(st: EngineBatch): Promise<void> {
  const f = st.files?.[0];
  const entry = shelvedEntry(getState(), st.id);
  if (!f || entry?.k !== "file") return;
  const saved = entry.saved as Single;
  if (f.state === "unprocessable" || f.state === "skipped") {
    await abandonSingle(saved.runId, (reasonLabel(f.detail) ?? "文件无法处理") + " · " + saved.name);
    return;
  }
  if (f.state === "cancelled") return; // only discarding a task cancels it, and that takes the entry away when it is through
  const ended = f.state === "completed" || f.state === "partial";
  store.update((d) => {
    const h = shelvedEntry(d, st.id);
    if (!h) return;
    const x = h.saved as Single;
    x.live!.job = f.id;
    x.live!.locked = passwordWait(f);
    if (ended) {
      x.phase = "done";
      x.live!.output = f.output;
      x.live!.digest = f.digest;
      const partial = f.state === "partial";
      Object.assign(h, { st: partial ? "part" : "done", stl: partial ? "部分结果" : "已导出", undo: undefined, park: undefined });
      return;
    }
    if (x.phase === "exporting" && (f.state === "awaiting_review" || f.state === "failed" || f.state === "blocked")) x.phase = "review";
    Object.assign(h, singleEntryStatus(x, f.state));
  });
  if (ended) toast("「" + saved.name + "」已在后台完成");
}

async function syncSingle(st: EngineBatch): Promise<void> {
  const f = st.files?.[0];
  const s = getState().single;
  // Answers to a call can come after the screen moved on to another task, which goes on under “最近”. A file opened
  // from a batch is not this task either: the batch's events are about its other files too.
  if (!s?.live || s.live.batch !== st.id) { await syncShelvedSingle(st); return; }
  if (!f || s.from === "batch") return;
  const runId = s.runId;
  const locked = passwordWait(f);
  if (locked !== !!s.live.locked || f.id !== s.live.job) store.update((d) => { if (d.single?.runId === runId) { d.single.live!.locked = locked; d.single.live!.job = f.id; } });
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
      if (loading.has(runId)) return;
      loading.add(runId);
      try { await showReview(runId, f.id); } finally { loading.delete(runId); }
      return;
    }
    const [stage, sp] = PROGRESS[f.state] ?? [0, 50];
    store.update((d) => { if (d.single?.runId === runId) { d.single.stage = stage; d.single.sp = sp; } });
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
 *  that stopped before its document was read has nothing to go back to, and it is given up. */
async function reviewStopped(runId: number, f: EngineFile): Promise<void> {
  if (loading.has(runId)) return;
  loading.add(runId);
  const name = getState().single?.name;
  try {
    let read: boolean;
    try {
      read = (await liveEngine()!.documentUnits(f.id, 0, 1)).length > 0;
    } catch (err) {
      shelveUnread(runId, err);
      return;
    }
    if (!read) { await abandonSingle(runId, (reasonLabel(f.detail) ?? "文件无法处理") + " · " + name); return; }
    await showReview(runId, f.id);
    if (getState().single?.runId === runId) toast(stoppedText(f, "处理未完成"));
  } finally {
    loading.delete(runId);
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
async function openRaster(job: string, stale: () => boolean): Promise<{ doc: RasterDoc; pages: number } | null> {
  try {
    const first = await liveEngine()!.pagePreview(job, 1, RASTER_DPI);
    return { doc: { pages: { 0: rasterPage(first) }, failed: {} }, pages: first.pages };
  } catch {
    if (!stale()) toast("页面预览不可用，改为文本显示");
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
  const stale = () => getState().single?.runId !== runId; // the screen went on to another task: nothing to tell it
  try {
    const { units, truncated } = await loadUnits(job);
    const doc = buildLiveDocument(units, s.ext);
    const raster = s.ext === "pdf" ? await openRaster(job, stale) : null;
    if (raster) padPages(doc.pages, doc.seqOf, raster.pages);
    store.update((d) => {
      const x = d.single;
      if (!x || x.runId !== runId) return;
      Object.assign(x, { pages: doc.pages, hits: doc.hits, imgs: [], phase: "review", stage: 0, sp: 0 });
      x.live = { ...x.live!, job, findingOf: doc.findingOf, seqOf: doc.seqOf, truncated, raster: raster?.doc };
    });
    if (truncated && !stale()) toast("文档较长，只显示前一部分；其余内容按方案自动处理");
  } catch (err) {
    shelveUnread(runId, err);
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
    await sendDecisions(job, decisionsOf(s.hits, s.live));
    await e.publish(batch, false, out);
    await e.retry(batch, [job]); // a file that stopped goes again; one that did not is left as it is
    await syncSingle(await e.status(batch, 10));
  } catch (err) {
    store.update((d) => {
      const x = d.single?.runId === runId ? d.single : d.behind?.single.runId === runId ? d.behind.single : null;
      if (x) { x.phase = "review"; return; }
      const h = shelvedRun(d, runId);
      if (h) { (h.saved as Single).phase = "review"; Object.assign(h, singleEntryStatus(h.saved as Single)); }
    });
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
  if (e && batch) void e.cancel(batch).then(() => cleanWhenStopped(batch)).catch(() => {});
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
      // nothing is left to undo, but the choices made before the selection still have to reach the kernel
      x.live = { ...x.live!, findingOf: doc.findingOf, seqOf: doc.seqOf, truncated, sent: true };
      d.undo = [];
    });
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** Decisions of a file opened from a batch are written to the kernel when the person leaves it. Whether it took them
 *  comes back; a refusal has been told by then. */
export function flushLiveDecisions(name: string, job: string, hits: Single["hits"], findingOf: number[]): Promise<boolean> {
  return sendDecisions(job, decisionsOf(hits, { findingOf })).then(() => true, (err) => {
    toast("「" + name + "」的选择未能保存：" + errorText(err));
    return false;
  });
}

/** “跳过此文件”: the file is left out of the batch and not exported. False when the kernel refused. */
export async function skipLive(job: string): Promise<boolean> {
  const e = liveEngine();
  if (!e) return false;
  try {
    await e.skip(job);
    void forgetDecisions(job);
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
  store.update((d) => { bringBackBehind(d); d.view = "batch"; });
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
  store.update((d) => { bringBackBehind(d); d.view = "batch"; });
  try {
    await sendDecisions(job, ds);
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
    st = await e.importFiles({ files: paths, quick: false, outputDir, preset: presetEngineId(getState().homePreset) });
  } catch (err) {
    toast(errorText(err));
    return;
  }
  let shelved = false;
  let working = false;
  store.update((d) => {
    leaveLiveFile(d); // a file of the batch that leaves the screen must not stay open
    shelved = setAsideBatch(d);
    working = d.history[0]?.st === "run";
    const b: Batch = {
      title: names.length + " 份文档", n: names.length, files, groups: [], phase: "precheck", mode: "guided", preset: d.homePreset, modelId: null,
      imgAct: "remove", out: outputDir, autoPub: true, keep: "7", paused: false, tab: "todo", ff: "all", q: "",
      live: { id: st.id, jobs: [], outputDir },
    };
    d.batch = b;
    d.view = "batch";
  });
  if (shelved) toast(leftBehind("上一个批次", working));
  try {
    await syncBatch(await e.preflight(st.id));
  } catch (err) {
    store.update((d) => {
      if (d.batch?.live?.id === st.id) { d.batch = null; d.view = "home"; }
      else d.history = d.history.filter((h) => !(h.park && savedBatchId(h.saved) === st.id));
    });
    toast(errorText(err));
  }
}

/** Per batch: the one being taken in and the newest picture waiting for it. A batch never waits for another's rows. */
const syncing = new Set<string>();
const newest = new Map<string, EngineBatch>();

/** Takes the kernel's picture of a batch in, wherever the batch is by the time its rows are read. */
async function syncBatch(st: EngineBatch): Promise<void> {
  newest.set(st.id, st);
  if (syncing.has(st.id)) return;
  syncing.add(st.id);
  try {
    for (let cur = newest.get(st.id); cur; cur = newest.get(st.id)) {
      newest.delete(st.id);
      await takeBatch(cur);
    }
  } finally {
    syncing.delete(st.id);
    newest.delete(st.id);
  }
}

async function takeBatch(cur: EngineBatch): Promise<void> {
  const s = getState();
  if (s.batch?.live?.id !== cur.id) {
    const entry = shelvedEntry(s, cur.id);
    if (!entry) return;
    const saved = entry.saved as Batch;
    if (saved.phase === "precheck") return; // it waits to be started: its rows are read when it comes back
    if (cur.active && !saved.paused) { // a large batch is not read in full for every step of work nobody is looking at
      store.update((d) => {
        const h = shelvedEntry(d, cur.id);
        if (h) Object.assign(h, { st: "run", stl: "处理中 · " + fmt(cur.delivered) + " / " + fmt(cur.total) });
      });
      return;
    }
  }
  const rows = await loadRows(cur.id, cur.total, cur.files ?? undefined);
  let finished = "";
  store.update((d) => {
    if (d.batch?.live?.id === cur.id) { takeRows(d.batch, rows, cur.total); return; }
    const h = shelvedEntry(d, cur.id);
    if (!h) return;
    const saved = h.saved as Batch;
    takeRows(saved, rows, cur.total);
    const status = batchEntryStatus(saved);
    Object.assign(h, status);
    if (!status.park) finished = saved.title;
  });
  if (finished) toast("「" + finished + "」已在后台完成");
}

/** The rows, the groups and the phase of a batch, from what the kernel reports. */
function takeRows(b: Draft<Batch>, rows: EngineFile[], total: number): void {
  const begun = b.phase !== "precheck";
  b.files = rows.map((r, i) => toBatchFile(b.files[i] ?? placeholderFile(i, r.name), r));
  b.live!.jobs = rows.map((r) => r.id);
  b.groups = liveGroups(b.files, rows);
  b.n = total;
  if (begun && (b.phase === "run" || b.phase === "result")) b.phase = isSettled(b) ? "result" : "run";
  if (!begun) b.files.forEach((f) => { if (f.st === "held" || f.st === "proc") f.st = f.st === "proc" ? "queue" : f.st; });
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
    store.update((d) => {
      if (d.batch?.live?.id === id) { d.batch.phase = "precheck"; return; }
      const h = shelvedEntry(d, id);
      if (h) { (h.saved as Batch).phase = "precheck"; Object.assign(h, batchEntryStatus(h.saved as Batch)); }
    });
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
  const id = b.live.id;
  closeModal();
  if (getState().publishWaiting.includes(id)) return; // the last click is still on its way
  // The publish waits for the writes on their way. A choice made on a file meanwhile would come too late for it, so no
  // file opens until it has gone, not one still being read either.
  store.update((d) => { d.publishWaiting.push(id); });
  supersedeOpen();
  try {
    if (!(await decisionsLanded(b))) return; // it would publish what was built from the choices before them
    await e.publish(id, b.imgAct === "keep", b.live.outputDir);
    toast("已开始导出");
  } catch (err) {
    toast(errorText(err));
  } finally {
    store.update((d) => { d.publishWaiting = d.publishWaiting.filter((x) => x !== id); });
  }
}

export function toggleLivePause(): void {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) return;
  const id = b.live.id;
  const pause = !b.paused;
  store.update((d) => { d.batch!.paused = pause; });
  (pause ? e.pause(id) : e.resume(id)).then(
    () => toast(pause ? "已暂停，进行中的步骤会回到起点" : "已继续"),
    (err) => {
      store.update((d) => {
        if (d.batch?.live?.id === id) { d.batch.paused = !pause; return; }
        const h = shelvedEntry(d, id);
        if (h) { (h.saved as Batch).paused = !pause; Object.assign(h, batchEntryStatus(h.saved as Batch)); }
      });
      toast(errorText(err));
    },
  );
}

/** Stops the batch. Keeping it leaves the kernel's data for a later resume; cleaning removes everything. */
export async function stopLiveBatch(keep: boolean): Promise<void> {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) return;
  const id = b.live.id;
  const { title } = b;
  await allDecisionsSettled();
  try {
    if (keep) {
      await e.pause(id);
      store.update((d) => {
        // it may have left the screen while the writes landed: then it is listed already
        const h = shelvedEntry(d, id);
        if (h) { (h.saved as Batch).paused = true; Object.assign(h, { st: "keep", stl: "已暂停 · 可继续" }); return; }
        const x = d.batch;
        if (x?.live?.id !== id) return;
        x.paused = true;
        d.history.unshift({ id: nextId("h"), k: "batch", name: x.title, meta: x.n + " 份", st: "keep", stl: "已暂停 · 可继续", when: "刚刚", saved: plain(x), park: true });
        d.batch = null;
        d.view = "home";
      });
      toast("已暂停，可在“最近”中继续");
    } else {
      await e.cancel(id);
      await cleanWhenStopped(id);
      store.update((d) => {
        d.history = d.history.filter((h) => !(h.park && savedBatchId(h.saved) === id));
        if (d.batch?.live?.id === id) { d.batch = null; d.view = "home"; }
      });
      toast("已停止并清理「" + title + "」，其他任务保留；已导出的文件不受影响");
    }
  } catch (err) { toast(errorText(err)); }
}

/** Removes a stopped batch's data; the kernel refuses until every file has come to rest, so it is asked again. */
function cleanWhenStopped(id: string): Promise<void> {
  return askUntilStopped(() => liveEngine()!.clean(id));
}

async function askUntilStopped(clean: () => Promise<void>): Promise<void> {
  let last: unknown;
  for (let i = 0; i < 100; i++) {
    try { await clean(); return; } catch (err) { last = err; await new Promise((r) => setTimeout(r, 100)); }
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
  if (!entry || !saved?.live) return;
  store.update((d) => {
    shelveScreenSingle(d);
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

/** Stops a task the kernel keeps and removes its data. False if it refused. */
export async function discardLiveTask(batch: string): Promise<boolean> {
  const e = liveEngine();
  if (!e) return false;
  await allDecisionsSettled(); // a write that lands after the data went has nothing to write to
  try {
    await e.cancel(batch);
    await cleanWhenStopped(batch);
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** Stops one file of a batch and removes its data; the rest of the batch stays. False if the kernel refused. */
async function discardLiveFile(job: string): Promise<boolean> {
  const e = liveEngine();
  if (!e) return false;
  try {
    await e.cancelFile(job);
    await askUntilStopped(() => e.cleanFile(job));
    await forgetDecisions(job); // its row stays in the batch: what was refused on it no longer holds the batch back
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** “停止并清理” with choices the kernel refused: those files go, a single task as a whole, and every other task is kept
 *  as “保存并关闭” keeps it, with what only this window holds handed to the kernel first. `named` is what the person was
 *  told goes: it does, even if a write of it has landed since. A file refused while this saves goes too: there is no
 *  window left to hold its choices. */
export async function discardRefused(named: RefusedFile[]): Promise<void> {
  openDecisions(getState()).forEach((o) => {
    void sendDecisions(o.job, decisionsOf(o.hits, { findingOf: o.findingOf })).catch(() => {});
  });
  await allDecisionsSettled();
  const late = refusedFiles(getState(), refusedJobs()).filter((f) => !named.some((n) => n.job === f.job));
  const files = [...named, ...late];
  // a task the kernel would not give up stays where it is, as it does when discarded from its entry
  const gone = await Promise.all(files.map(async (f) => (f.inBatch ? discardLiveFile(f.job) : (await discardLiveTask(f.batch)) && f.batch)));
  const whole = new Set(gone.filter((g) => typeof g === "string"));
  store.update((d) => {
    if (d.single?.live && whole.has(d.single.live.batch)) { d.single = null; d.undo = []; d.view = "home"; }
    d.history = d.history.filter((h) => !(h.park && whole.has(savedBatchId(h.saved) ?? "")));
  });
}

/** Gives up a batch that has not started. */
export function cancelLiveBatch(): void {
  const e = liveEngine();
  const id = getState().batch?.live?.id;
  if (e && id) void e.cancel(id).then(() => cleanWhenStopped(id)).catch(() => {});
}

/** False when nothing was queued, having said why. */
export async function retryLive(fi?: number): Promise<boolean> {
  const e = liveEngine()!;
  const b = getState().batch;
  if (!b?.live?.id) return false;
  if (!(await decisionsLanded(b))) return false; // it would go on from what was built from the choices before them
  try {
    const only = fi == null ? [] : [b.live.jobs[fi]!];
    await e.retry(b.live.id, only);
    return true;
  } catch (err) {
    toast(errorText(err));
    return false;
  }
}

/** The click on a file of the batch that counts: only the last one may change the screen. */
let lastOpen = 0;

/** A new click: what is still being read for an earlier one is dropped when it arrives. */
function supersedeOpen(): number {
  lastOpen = nextRunId();
  if (getState().fileOpening) store.update((d) => { d.fileOpening = null; });
  return lastOpen;
}

/** Opens the document of one file of the batch for review, or the dialog it is waiting for. */
export async function openLiveFile(fi: number): Promise<void> {
  const runId = supersedeOpen();
  const superseded = () => lastOpen !== runId;
  const s = getState();
  const b = s.batch!;
  const f = b.files[fi]!;
  const batchId = b.live!.id;
  // What is read arrives late. It is dropped, without a word, if the person has moved on: another click, another batch,
  // another page. The page is compared, not told: every way of leaving it changes the view.
  const stale = () => superseded() || getState().batch?.live?.id !== batchId || getState().view !== s.view;
  const job = b.live!.jobs[fi]!;
  closeModal();
  if (s.publishWaiting.includes(batchId)) { toast("正在导出，暂时不能打开文件"); return; }
  if (f.g.includes(GROUP_PASSWORD)) { openModal({ type: "livePassword", targets: [{ job, name: f.name }] }); return; }
  if (f.g.includes(GROUP_BLOCKED) || f.st === "bad") { toast((f.reason ?? "无法处理") + " · " + f.name); return; }
  if (f.st === "skipped") { toast("已跳过 · 不会导出"); return; }
  if (f.st === "done" || f.st === "partial") { toast("已导出为 " + (f.outputName ?? f.id)); return; }
  if (!(f.g.includes(GROUP_REVIEW) || f.g.includes(GROUP_STRICT) || f.st === "ready")) { toast("尚未检测完成"); return; }
  store.update((d) => { d.fileOpening = { batch: batchId, name: f.name }; });
  try {
    await decisionsSettled(job); // what the document says comes from the kernel: the last choices must be there first
    const { units, truncated } = await loadUnits(job);
    if (stale()) return;
    // what the kernel refused is still what the person chose: it shows, and leaving the file sends it again
    const unsaved = unsavedDecisions(job);
    const doc = buildLiveDocument(unsaved ? withDecisions(units, unsaved) : units, f.ext);
    const raster = f.ext === "pdf" ? await openRaster(job, stale) : null;
    if (stale()) return;
    if (raster) padPages(doc.pages, doc.seqOf, raster.pages);
    store.update((d) => {
      leaveLiveFile(d); // a file still on screen gives way: its choices exist nowhere else
      if (d.single && d.single.from !== "batch" && !d.behind) d.behind = { single: plain(d.single), undo: plain(d.undo) };
      d.single = createSingle(f.name, f.ext, undefined, { mode: d.docMode, preset: d.batch!.preset, modelId: null }, {
        phase: "review", from: "batch", bfi: fi, runId, size: "", out: d.batch!.out, pages: doc.pages, hits: doc.hits, imgs: [],
        live: { batch: batchId, job, findingOf: doc.findingOf, seqOf: doc.seqOf, truncated, raster: raster?.doc, sent: !!unsaved },
      });
      d.undo = [];
      d.view = "single";
    });
    const notes = [unsaved && "这是上次未能保存的选择，离开文件时会重新保存", truncated && "文档较长，只显示前一部分；其余内容按方案自动处理"].filter(Boolean);
    if (notes.length) toast(notes.join(" · "));
  } catch (err) {
    if (!stale()) toast(errorText(err));
  } finally {
    if (!superseded()) store.update((d) => { d.fileOpening = null; }); // a later click has its own to show
  }
}

/** A group's row was clicked: open its first file, ask for its passwords, or retry it. */
export function openLiveGroup(gid: string | null): void {
  const b = getState().batch!;
  const g = gid ? b.groups.find((x) => x.id === gid) : b.groups.find((x) => Object.keys(x.per).length);
  if (!g) return;
  supersedeOpen();
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

/** Puts back what the last run left unfinished: the newest quick task continues in its window; the other quick tasks
 *  and every batch are listed under “最近” and continue when chosen. Nothing else runs until the person chooses (the
 *  kernel keeps it paused). */
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
  // the kernel lists them in the order they were imported
  const newestQuick = list.filter((b) => b.kind === "quick" && b.files?.length).at(-1);
  for (const b of list) {
    const s = getState();
    if (tracked(s, b.id)) continue;
    if (b.kind === "quick") {
      if (!b.files?.length) continue;
      if (b !== newestQuick || s.single) listQuick(b);
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
    d.history.unshift({ id: nextId("h"), k: "file", ext: f.format, name, meta: b.id, st: "warn", stl: b.parked ? "待确认" : "已暂停 · 可继续", when: "上次", saved, undo: [], park: true });
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
    shelveUnread(runId, err);
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
