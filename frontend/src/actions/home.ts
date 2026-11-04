// The home page: taking files, making room on screen, and the list of recent tasks.
import { keepDays } from "../domain/batch";
import { extOf, parseCount } from "../domain/format";
import { presetModelId } from "../domain/presets";
import { createSingle } from "../domain/single";
import type { BatchSummary, HistoryEntry, Single } from "../domain/types";
import { getState, store } from "../store";
import { isWorking, liveHistory, screenSingle, unfinishedOnScreen } from "../store/selectors";
import { closeModal, closePop, openModal, toast } from "../overlay/store";
import { archiveBatch, leaveBatchFile, newBatch, parkBatch, resumeBatch } from "./batch";
import { discardLiveTask, reopenLiveSingle, savedBatchId, setAsideBatch, shelveScreenSingle } from "./live";
import { needModel } from "./models";
import { newSingle } from "./single";

export interface IncomingFile {
  name: string;
  size: number;
}

export function takeFiles(files: IncomingFile[]): void {
  if (!files.length) return;
  const s = getState();
  if (s.modelPending) { toast("请先继续或取消已选择的操作，再选择其他文档"); return; }
  const preset = s.homePreset;
  const modelId = presetModelId(s.presets, preset);
  if (files.length === 1) {
    const f = files[0]!;
    const go = () => makeRoom("single", () => newSingle(f.name, f.size, { preset, modelId }));
    if (needModel(preset, go, f.name, modelId)) go();
  } else makeRoom("batch", () => newBatch(files.map((f) => f.name)));
}

/**
 * One single task and one batch on screen at a time. The one on screen gives way without asking and stays under
 * “最近” (FR-28), where the kernel goes on with any work it has. Only the tasks of the demonstration, whose clock runs
 * on the screen and which no kernel keeps, still wait or ask.
 */
export function makeRoom(kind: "single" | "batch", go: () => void): void {
  if (kind === "single") { makeRoomForSingle(go); return; }
  const s = getState();
  if (s.batch?.live) {
    store.update((d) => { leaveBatchFile(d); setAsideBatch(d); });
    go();
    return;
  }
  if (!unfinishedOnScreen(s, "batch")) {
    if (s.batch) archiveBatch();
    go();
    return;
  }
  openModal({ type: "slotConflict", go });
}

function makeRoomForSingle(go: () => void): void {
  const cur = screenSingle(getState());
  if (cur && !cur.live && isWorking(cur)) { toast("「" + cur.name + "」正在处理，请稍候"); return; }
  if (cur && cur.phase !== "done") {
    store.update((d) => { leaveBatchFile(d); shelveScreenSingle(d); });
    toast(isWorking(cur) ? "上一份文件继续在后台处理，可在“最近”查看" : "已保存到“最近”");
  }
  go();
}

/** Choose “保存并继续” in the slot dialog of a demonstration batch: keep it and carry on with `go`. */
export function parkAndContinue(go: () => void): void {
  closeModal();
  const s = getState();
  if (s.batch!.phase === "precheck") {
    store.update((d) => { d.batch = null; });
  } else {
    const days = keepDays(s.batch);
    parkBatch(days);
    toast("当前批次已保存 " + days + " 天，可在“最近”中继续");
  }
  go();
}

/** Back to the batch on screen. */
export function viewSlotTask(): void {
  closeModal();
  store.update((d) => { d.view = "batch"; });
}

/* ── Recent tasks ─────────────────────────────────────────────────────────── */

/** Completed history is viewed separately so active tasks keep their screens and timers. */
export function openHistoryResult(entry: HistoryEntry): void {
  const live = entry.live === "batch";
  let data: Single | BatchSummary | null = live ? null : (entry.saved as Single | BatchSummary | undefined) ?? null;
  if (!live && !data) {
    if (entry.k === "file") {
      data = createSingle(entry.name, entry.ext || extOf(entry.name), undefined, { mode: "orig", preset: "办公外发", modelId: null }, { phase: "done" });
    } else {
      const n = parseCount(entry.meta);
      data = {
        title: entry.name, n, out: "桌面 / 已脱敏", phase: "result",
        files: Array.from({ length: n }, (_, i) => ({ i, id: "T" + String(i + 1).padStart(4, "0"), ext: "pdf", st: "done" as const })),
      };
    }
  }
  store.update((d) => {
    if (!live && !entry.saved) { const h = d.history.find((x) => x.id === entry.id); if (h) h.saved = data!; }
    d.historyResult = { entry: { ...entry, saved: undefined }, data, phase: "result" };
    d.navStack = [];
    d.view = "history";
  });
}

export function openHistoryEntry(id: string): void {
  closeModal();
  closePop();
  const s = getState();
  const x = liveHistory(s).find((h) => h.id === id);
  if (!x) return;
  // A parked task is not a result, whatever its status says about what it delivered so far.
  if (!x.park && (["done", "part"].includes(x.st) || x.stl.startsWith("已导出"))) { openHistoryResult(x); return; }
  if (x.live) { store.update((d) => { d.view = x.live!; }); return; }
  if (x.park && savedBatchId(x.saved)) { openModal({ type: "resumeBatch", entryId: x.id }); return; }
  if (x.park) {
    makeRoom("single", () => store.update((d) => {
      d.history = d.history.filter((h) => h.id !== x.id);
      d.single = x.saved as Single;
      d.undo = x.undo || [];
      d.view = "single";
    }));
    return;
  }
  if (x.saved && x.k === "file") {
    makeRoom("single", () => store.update((d) => { d.single = x.saved as Single; d.undo = []; d.view = "single"; }));
    return;
  }
  if (x.st === "keep") { openModal({ type: "resumeBatch", entryId: x.id }); return; }
  if (x.saved && x.k === "batch") { makeRoom("batch", () => resumeBatch(x.id)); return; }
  openModal({ type: "historyItem", entryId: x.id });
}

/** “继续处理” in the dialog of a saved task. */
export function resumeSavedBatch(entryId: string): void {
  closeModal();
  const x = getState().history.find((h) => h.id === entryId);
  if (x?.k === "file") { void reopenLiveSingle(entryId); return; }
  makeRoom("batch", () => { resumeBatch(entryId); toast("已恢复，继续处理"); });
}

/** “停止并清理”. What the kernel keeps goes first: a task it would not give up stays listed. */
export async function discardSavedTask(entryId: string): Promise<void> {
  const x = getState().history.find((h) => h.id === entryId);
  closeModal();
  if (!x) return;
  const batch = savedBatchId(x.saved);
  if (batch && !(await discardLiveTask(batch))) return;
  store.update((d) => { d.history = d.history.filter((h) => h.id !== entryId); });
  toast("已清理「" + x.name + "」");
}

export function setHistoryPhase(phase: "result" | "manifest"): void {
  closeModal();
  store.update((d) => { d.historyResult!.phase = phase; });
}

export function setHomePreset(name: string): void {
  store.update((d) => { d.homePreset = name; });
}
