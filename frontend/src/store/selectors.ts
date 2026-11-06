// Derived facts about the application state. Pure functions of `AppState`, usable from actions and components.
import { batchStatus, isSettled } from "../domain/batch";
import { fmt } from "../domain/format";
import { modelById, presetModelId } from "../domain/presets";
import type { Batch, HistoryEntry, ModelPlugin, Single } from "../domain/types";
import type { AppState } from "./state";

export const LIVE_BATCH_ID = "live-batch";
export const LIVE_SINGLE_ID = "live-single";

/** The single-file task on screen: while a file of a batch is open, the one waiting behind it. */
export function screenSingle(s: Pick<AppState, "single" | "behind">): Single | null {
  return s.single && s.single.from === "batch" ? s.behind?.single ?? null : s.single;
}

/** A saved task that can be continued, so its recovery data is what cleaning the workspace removes. */
export const isRecoverable = (h: HistoryEntry): boolean => !!h.park || h.st === "keep";

/** A single task the kernel is working on, as far as this window knows; a password stops it. */
export const isWorking = (s: Pick<Single, "phase" | "live">): boolean =>
  s.phase === "exporting" || (s.phase === "processing" && !s.live?.locked);

/** The unfinished task of a kind on screen, or null if there is nothing it would be lost to close. */
export function unfinishedOnScreen(s: AppState, kind: "single"): Single | null;
export function unfinishedOnScreen(s: AppState, kind: "batch"): Batch | null;
export function unfinishedOnScreen(s: AppState, kind: "single" | "batch"): Single | Batch | null {
  if (kind === "single") {
    const single = screenSingle(s);
    return single && single.phase !== "done" ? single : null;
  }
  const b = s.batch;
  return b && !(["result", "manifest"].includes(b.phase) && isSettled(b)) ? b : null;
}

/** The batch on screen while something of it is open, even when nothing runs: it is not left without a word. */
export function unfinishedBatch(s: AppState): Batch | null {
  return unfinishedOnScreen(s, "batch") ?? (s.batch && batchStatus(s.batch)[0] === "warn" ? s.batch : null);
}

/** Every task still kept for the person, each once: a file of a batch that is open is part of that batch. */
export function keptTasks(s: Pick<AppState, "batch" | "single" | "behind" | "history">): (Single | Batch)[] {
  const parked = s.history.filter((h) => h.park).map((h) => h.saved as Single | Batch | undefined);
  return [s.batch, s.single, s.behind?.single, ...parked].filter((t): t is Single | Batch => !!t?.live && (t as Single).from !== "batch");
}

/** What “停止并清理” removes for a file whose choices the kernel refused: the file, and with a single task the task it is. */
export interface RefusedFile {
  job: string;
  name: string;
  /** The kernel's batch of the task. */
  batch: string;
  /** The batch the file is one of; absent for a single task, which goes as a whole. */
  inBatch?: string;
}

/** The files among `jobs` that belong to a task still kept, in the order of `keptTasks`. */
export function refusedFiles(s: Pick<AppState, "batch" | "single" | "behind" | "history">, jobs: readonly string[]): RefusedFile[] {
  return keptTasks(s).flatMap((t): RefusedFile[] => {
    if ("files" in t) {
      const live = t.live!;
      return live.jobs.flatMap((job, i) => (jobs.includes(job) ? [{ job, name: t.files[i]?.name ?? job, batch: live.id, inBatch: t.title }] : []));
    }
    const live = t.live!;
    return jobs.includes(live.job) ? [{ job: live.job, name: t.name, batch: live.batch }] : [];
  });
}

/** The tasks on screen, followed by the ones under “最近”. */
export function liveHistory(s: Pick<AppState, "batch" | "single" | "behind" | "history">): HistoryEntry[] {
  const out: HistoryEntry[] = [];
  const b = s.batch;
  const single = screenSingle(s);
  if (b) {
    const [st, stl] = batchStatus(b);
    out.push({ id: LIVE_BATCH_ID, k: "batch", name: b.title, meta: fmt(b.n) + " 份", st, stl, when: "刚刚", live: "batch" });
  }
  if (single && single.phase !== "done") {
    out.push({
      id: LIVE_SINGLE_ID, k: "file", ext: single.ext, name: single.name, meta: single.pages.length + " 页",
      st: single.phase === "review" ? "warn" : "run", stl: single.phase === "review" ? "待确认" : "处理中", when: "刚刚", live: "single",
    });
  }
  return out.concat(s.history);
}

export const modelIsBusy = (models: ModelPlugin[], id?: string | null): boolean =>
  (id ? [modelById(models, id)] : models).some((m) => m && ["downloading", "verifying", "loading"].includes(m.status));

/** The model the current screen is about (for the buttons of a model card that names none). */
export function contextModelId(s: AppState): string | null | undefined {
  if (s.modelPending) return s.modelPending.modelId;
  if (s.view === "batch" && s.batch) return s.batch.modelId || presetModelId(s.presets, s.batch.preset);
  if (s.view === "single" && s.single) return s.single.modelId || presetModelId(s.presets, s.single.preset);
  return presetModelId(s.presets, s.homePreset);
}

/** A document is being processed, on screen or not, so the model cannot be changed underneath it. */
export function documentBusy(s: AppState): boolean {
  return (
    !!(s.single && ["processing", "exporting"].includes(s.single.phase)) ||
    !!(s.behind && isWorking(s.behind.single)) ||
    !!(s.batch && s.batch.phase !== "precheck" && s.batch.files.some((f) => ["proc", "queue", "ready"].includes(f.st))) ||
    s.history.some((h) => h.park && h.st === "run")
  );
}

/** Home shows the model guide when no model is ready or something about one needs attention. */
export function pendingBatchDot(s: AppState): boolean {
  return !!s.batch && s.view !== "batch" && batchStatus(s.batch)[0] === "warn";
}
