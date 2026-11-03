// Moving between pages. The pages are a flat set of views plus a stack for pages opened from another page.
import { store, type AppState } from "../store";
import { closeModal, closePop } from "../overlay/store";
import type { View } from "../domain/types";
import { leaveBatchFile, reportLeftBatchFile } from "./batch";

/** Opens `view` on top of the current one so “返回” leads back. */
export function openSubpage(view: View, patch: Partial<Pick<AppState, "presetTab" | "presetSel" | "rulesetSel" | "modelSel">> = {}): void {
  store.update((d) => {
    d.navStack.push({ view: d.view, presetTab: d.presetTab, presetSel: d.presetSel, rulesetSel: d.rulesetSel, modelSel: d.modelSel });
    d.view = view;
    Object.assign(d, patch);
  });
}

/** Goes to a top-level page. Leaving the review of a batch file writes its edits back. */
export function goTo(view: View): void {
  closeModal();
  closePop();
  let left: ReturnType<typeof leaveBatchFile> = null;
  store.update((d) => {
    left = leaveBatchFile(d);
    d.navStack = [];
    d.view = view;
  });
  reportLeftBatchFile(left, "page");
}

/** “返回” of a page opened with `openSubpage`. */
export function goBack(): void {
  closeModal();
  closePop();
  store.update((d) => {
    const r = d.navStack.pop();
    if (r) Object.assign(d, r);
    else d.view = "home";
  });
}
