// Local model preparation: download, pause, verify, trial run, and operations that wait for a model.
import { modelById, presetModelId } from "../domain/presets";
import { getState, store } from "../store";
import { contextModelId, documentBusy, modelIsBusy } from "../store/selectors";
import { closeModal, closePop, toast } from "../overlay/store";
import { setContinuation, takeContinuation } from "./pending";
import { stopAllModelTimers, stopModelTimer, timers } from "./timers";
import { openSubpage } from "./navigation";
import { requestScroll } from "./scrollRequests";
import type { ModelStatus } from "../domain/types";

function tickModel(id: string): void {
  const m = modelById(getState().models, id);
  if (!m) return;
  let readyName: string | null = null;
  store.update((d) => {
    const x = modelById(d.models, id)!;
    if (x.status === "downloading") {
      x.progress = Math.min(100, x.progress + 5);
      if (x.progress === 100) x.status = "verifying";
    } else if (x.status === "verifying") x.status = "loading";
    else if (x.status === "loading") {
      x.status = "ready";
      readyName = x.name;
    }
  });
  if (readyName) {
    stopModelTimer(id);
    toast(readyName + " 已就绪" + (getState().modelPending?.modelId === id ? "，可以继续处理" : "，可以离线使用"));
  }
}

export function downloadModel(id: string | null | undefined = contextModelId(getState())): void {
  const s = getState();
  const m = modelById(s.models, id);
  if (!m) return;
  if (documentBusy(s)) { toast("请先完成或停止当前文档处理，再下载模型"); return; }
  if (modelIsBusy(s.models, m.id)) return;
  stopModelTimer(m.id);
  store.update((d) => {
    const x = modelById(d.models, m.id)!;
    if (x.error === "integrity" || x.error === "load") x.progress = 0;
    x.error = null;
    x.status = "downloading";
  });
  timers.models.set(m.id, setInterval(() => tickModel(m.id), 500));
}

export function prepareModel(id: string | null | undefined = contextModelId(getState())): void {
  const s = getState();
  const m = modelById(s.models, id);
  if (!m) return;
  if (documentBusy(s)) { toast("请先完成或停止当前文档处理，再准备模型"); return; }
  stopModelTimer(m.id);
  store.update((d) => {
    const x = modelById(d.models, m.id)!;
    x.error = null;
    x.status = "loading";
  });
  timers.models.set(m.id, setInterval(() => tickModel(m.id), 900));
}

export function pauseModel(id: string | null | undefined = contextModelId(getState())): void {
  const m = modelById(getState().models, id);
  if (!m || m.status !== "downloading") return;
  stopModelTimer(m.id);
  store.update((d) => { modelById(d.models, m.id)!.status = "paused"; });
}

/** Opens the plugin page at the model section (or at one model's card). */
export function showModels(id?: string): void {
  closeModal();
  closePop();
  if (getState().view !== "plugins") openSubpage("plugins");
  requestScroll(id ? { kind: "model", id } : { kind: "models" });
}

export function cancelPendingOperation(): void {
  setContinuation(null);
  store.update((d) => { d.modelPending = null; });
}

export function continuePendingOperation(): void {
  const s = getState();
  const p = s.modelPending;
  if (!p || modelById(s.models, p.modelId)?.status !== "ready" || modelIsBusy(s.models)) return;
  const go = takeContinuation();
  store.update((d) => { d.modelPending = null; });
  go?.();
}

/**
 * Whether `go` can run now. If the model of the scheme is not ready, the operation is parked as pending (shown on the
 * model card) and `go` runs when the user continues.
 */
export function needModel(preset: string, go: () => void, label: string, boundId?: string | null): boolean {
  const s = getState();
  const id = boundId || presetModelId(s.presets, preset);
  const m = modelById(s.models, id);
  if ((!id || m?.status === "ready") && !modelIsBusy(s.models)) return true;
  if (s.modelPending) { toast("请先继续或取消上一次等待的操作"); return false; }
  const waitingId = id || s.models.find((x) => modelIsBusy(s.models, x.id))?.id;
  setContinuation(go);
  store.update((d) => { d.modelPending = { label, preset, modelId: waitingId }; });
  return false;
}

/** Puts all models into one status (scenes and tests). */
export function resetModels(status: ModelStatus = "ready"): void {
  stopAllModelTimers();
  setContinuation(null);
  store.update((d) => {
    d.models.forEach((m) => Object.assign(m, { status, progress: status === "ready" ? 100 : 0, error: null }));
    d.modelPending = null;
  });
}
