// Human review of batch exceptions (the screen with the queue, the document and the decision panel).
import { currentItem, decide, enterReview, resumeReview, selectItem, stepItem, undoDecision } from "../domain/review";
import type { ViewMode } from "../domain/types";
import { getState, store } from "../store";
import { closePop, toast } from "../overlay/store";
import { needSlot } from "./home";
import { openBatchFile, resumeBatch } from "./batch";
import { openLiveFile, openLiveGroup } from "./live";

/**
 * Opens the review at the first pending item, optionally of one group and/or one file. The review screen is the
 * demonstration's and its decisions reach nothing, so a batch of the kernel goes to the file or the dialog it waits for.
 */
export function enterBatchReview(gid?: string | null, fi?: number | null): void {
  closePop();
  if (getState().batch?.live) {
    if (fi != null) void openLiveFile(fi);
    else openLiveGroup(gid ?? null);
    return;
  }
  store.update((d) => { enterReview(d.batch!, gid, fi); });
}

/** “开始复核” / “去处理”. From a history result the batch may first have to take back its slot. */
export function startReview(gid?: string | null): void {
  const s = getState();
  if (s.view === "history") {
    const go = () => {
      store.update((d) => { d.view = "batch"; });
      enterBatchReview(gid);
    };
    if (s.historyResult!.data === null) go();
    else needSlot("batch", () => { resumeBatch(s.historyResult!.entry.id); go(); });
    return;
  }
  enterBatchReview(gid);
}

export function selectReviewItem(key: string): void {
  store.update((d) => { selectItem(d.batch!, key); });
}

export function resumeBatchReview(): void {
  store.update((d) => { resumeReview(d.batch!); });
}

export function stepReview(delta: number): void {
  store.update((d) => { if (d.batch?.rv) stepItem(d.batch, delta); });
}

export function decideItem(k: string): void {
  let result: ReturnType<typeof decide> = null;
  store.update((d) => { if (d.batch?.rv) result = decide(d.batch, k); });
  const r = result as ReturnType<typeof decide>;
  if (r?.toast) toast(r.toast, r.ok ? undoReview : undefined);
}

export function undoReview(): void {
  let result: ReturnType<typeof undoDecision> = null;
  store.update((d) => { if (d.batch?.rv) result = undoDecision(d.batch); });
  const r = result as ReturnType<typeof undoDecision>;
  if (r) toast(r.toast);
}

export function setReviewAll(all: boolean): void {
  store.update((d) => { d.batch!.rv!.all = all; });
}

export function setReviewMode(mode: ViewMode): void {
  store.update((d) => { d.docMode = mode; });
}

/** Hover or focus on a decision shows what it would do in the effect preview. */
export function previewReviewDecision(k: string | null): void {
  const rv = getState().batch?.rv;
  if (!rv || rv.hov === k) return;
  store.update((d) => { d.batch!.rv!.hov = k; });
}

export function openReviewedFile(): void {
  const b = getState().batch!;
  const it = currentItem(b);
  if (it) openBatchFile(it.fi);
}
