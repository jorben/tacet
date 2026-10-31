// The home page: taking files, the single/batch slots, and the list of recent tasks.
import { keepDays } from "../domain/batch";
import { extOf, parseCount } from "../domain/format";
import { presetModelId } from "../domain/presets";
import { createSingle } from "../domain/single";
import type { BatchSummary, HistoryEntry, Single } from "../domain/types";
import { getState, store } from "../store";
import { liveHistory, slotBusy } from "../store/selectors";
import { closeModal, closePop, openModal, toast } from "../overlay/store";
import { archiveBatch, newBatch, parkBatch, resumeBatch } from "./batch";
import { needModel } from "./models";
import { newSingle } from "./single";
import { nextId } from "./timers";

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
    const go = () => needSlot("single", () => newSingle(f.name, f.size, { preset, modelId }));
    if (needModel(preset, go, f.name, modelId)) go();
  } else needSlot("batch", () => newBatch(files.map((f) => f.name)));
}

/**
 * One single task and one batch at a time. An unfinished task is never replaced silently: the user chooses
 * between keeping it (saved under “最近”) and going back to it.
 */
export function needSlot(kind: "single" | "batch", go: () => void): void {
  const s = getState();
  const cur = kind === "single" ? slotBusy(s, "single") : slotBusy(s, "batch");
  if (!cur) {
    if (kind === "batch" && s.batch) archiveBatch();
    go();
    return;
  }
  if (kind === "single" && (cur as Single).phase !== "review") {
    toast("「" + (cur as Single).name + "」正在处理，请稍候");
    return;
  }
  openModal({ type: "slotConflict", kind, go });
}

/** Choose “保存并继续” in the slot dialog: park the busy task and carry on with `go`. */
export function parkAndContinue(kind: "single" | "batch", go: () => void): void {
  closeModal();
  const s = getState();
  if (kind === "single") {
    parkSingle();
    toast("已保存到“最近”");
  } else if (s.batch!.phase === "precheck") {
    store.update((d) => { d.batch = null; });
  } else {
    const days = keepDays(s.batch);
    parkBatch(days);
    toast("当前批次已保存 " + days + " 天，可在“最近”中继续");
  }
  go();
}

/** Back to the task that holds the slot. */
export function viewSlotTask(kind: "single" | "batch"): void {
  closeModal();
  store.update((d) => { d.view = kind; });
}

export function parkSingle(): void {
  store.update((d) => {
    const s = d.single!;
    d.history.unshift({ id: nextId("h"), k: "file", ext: s.ext, name: s.name, meta: s.pages.length + " 页", st: "warn", stl: "待确认", when: "刚刚", saved: s, undo: d.undo, park: true });
    d.single = null;
    d.undo = [];
  });
}

/* ── Recent tasks ─────────────────────────────────────────────────────────── */

/** Completed history is viewed separately so active tasks keep their slots and timers. */
export function openHistoryResult(entry: HistoryEntry): void {
  const live = entry.live === "batch";
  let data: Single | BatchSummary | null = live ? null : (entry.saved as Single | BatchSummary | undefined) ?? null;
  if (!live && !data) {
    if (entry.k === "file") {
      data = createSingle(entry.name, entry.ext || extOf(entry.name), undefined, { mode: "orig", preset: "通用办公脱敏", modelId: null }, { phase: "done" });
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
  if (["done", "part"].includes(x.st) || x.stl.startsWith("已导出")) { openHistoryResult(x); return; }
  if (x.live) { store.update((d) => { d.view = x.live!; }); return; }
  if (x.park) {
    needSlot("single", () => store.update((d) => {
      d.history = d.history.filter((h) => h.id !== x.id);
      d.single = x.saved as Single;
      d.undo = x.undo || [];
      d.view = "single";
    }));
    return;
  }
  if (x.saved && x.k === "file") {
    needSlot("single", () => store.update((d) => { d.single = x.saved as Single; d.undo = []; d.view = "single"; }));
    return;
  }
  if (x.st === "keep") { openModal({ type: "resumeBatch", entryId: x.id }); return; }
  if (x.saved && x.k === "batch") { needSlot("batch", () => resumeBatch(x.id)); return; }
  openModal({ type: "historyItem", entryId: x.id });
}

/** “继续处理” in the dialog of a saved batch. */
export function resumeSavedBatch(entryId: string): void {
  closeModal();
  needSlot("batch", () => { resumeBatch(entryId); toast("已恢复，继续处理"); });
}

export function discardSavedTask(entryId: string): void {
  const x = getState().history.find((h) => h.id === entryId);
  closeModal();
  store.update((d) => { d.history = d.history.filter((h) => h.id !== entryId); });
  if (x) toast("已清理「" + x.name + "」");
}

export function setHistoryPhase(phase: "result" | "manifest"): void {
  closeModal();
  store.update((d) => { d.historyResult!.phase = phase; });
}

export function setHomePreset(name: string): void {
  store.update((d) => { d.homePreset = name; });
}
