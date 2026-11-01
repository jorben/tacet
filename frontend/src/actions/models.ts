// Local model preparation: download, pause, verify, trial run, and operations that wait for a model.
import { modelById, presetModelId } from "../domain/presets";
import { getState, store } from "../store";
import { contextModelId, documentBusy, modelIsBusy } from "../store/selectors";
import { closeModal, closePop, toast } from "../overlay/store";
import { setContinuation, takeContinuation } from "./pending";
import { stopAllModelTimers, stopModelTimer, timers } from "./timers";
import { openSubpage } from "./navigation";
import { requestScroll } from "./scrollRequests";
import { getService } from "../services";
import type { ModelPlugin, ModelStatus } from "../domain/types";
import type { EngineService } from "../services/types";
import { refreshModels } from "./liveConfig";

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

/** Why the kernel refused a model operation, in words. */
const MODEL_REFUSED: Record<string, string> = {
  MODEL_MEMORY_INSUFFICIENT: "本机内存不足，无法使用此模型",
  MODEL_BUSY: "请先完成或停止当前文档处理，再下载模型",
  MODEL_OPERATION_IN_PROGRESS: "此模型正在处理中",
};

/** A model operation of the real kernel; the screens learn the outcome from the model's state. */
async function liveModel(m: ModelPlugin, op: (e: EngineService) => Promise<void>, busy?: ModelStatus): Promise<void> {
  const e = getService().engine!;
  try {
    await op(e);
    if (busy) store.update((d) => { Object.assign(modelById(d.models, m.id)!, { status: busy, error: null }); });
  } catch (err) {
    const code = String((err as { message?: string })?.message ?? err).replace(/^.*?([A-Z][A-Z0-9_]{2,})\b.*$/s, "$1");
    toast(MODEL_REFUSED[code] ?? m.name + " 操作未完成（" + code + "）");
  }
  await refreshModels();
}

export function downloadModel(id: string | null | undefined = contextModelId(getState())): void {
  const s = getState();
  const m = modelById(s.models, id);
  if (!m) return;
  if (documentBusy(s)) { toast("请先完成或停止当前文档处理，再下载模型"); return; }
  if (modelIsBusy(s.models, m.id)) return;
  if (m.eligible === false) { toast("本机内存不足 " + (m.minMemoryGiB || 16) + " GiB，无法下载此模型"); return; }
  if (getService().engine) {
    // a failed verification leaves nothing to resume: the download starts over
    void liveModel(m, async (e) => {
      if (m.status === "error" && m.error === "integrity") await e.discardModel(m.id);
      await e.downloadModel(m.id);
    }, "downloading");
    return;
  }
  stopModelTimer(m.id);
  store.update((d) => {
    const x = modelById(d.models, m.id)!;
    if (x.error === "integrity" || x.error === "load") x.progress = 0;
    x.error = null;
    x.status = "downloading";
  });
  timers.models.set(m.id, setInterval(() => tickModel(m.id), 500));
}

/** The GPU build of model m: download (again after a failed check), pause, or try it again. Live kernel only. */
export function downloadAccel(modelId: string): void {
  const s = getState();
  const m = modelById(s.models, modelId);
  const a = m?.runtime?.accel;
  if (!m || !a || !getService().engine) return;
  if (documentBusy(s)) { toast("请先完成或停止当前文档处理，再下载模型"); return; }
  void liveBuild(m, a.id, async (e) => {
    if (a.status === "error" && a.error === "integrity") await e.discardModel(a.id);
    await (a.status === "error" && a.error === "load" ? e.prepareModel(a.id) : e.downloadModel(a.id));
  }, a.status === "error" && a.error === "load" ? "loading" : "downloading");
}

export function pauseAccel(modelId: string): void {
  const m = modelById(getState().models, modelId);
  const a = m?.runtime?.accel;
  if (!m || !a || a.status !== "downloading" || !getService().engine) return;
  void liveBuild(m, a.id, (e) => e.pauseModel(a.id), "paused");
}

async function liveBuild(m: ModelPlugin, id: string, op: (e: EngineService) => Promise<void>, busy: ModelStatus): Promise<void> {
  try {
    await op(getService().engine!);
    store.update((d) => {
      const a = modelById(d.models, m.id)?.runtime?.accel;
      if (a) Object.assign(a, { status: busy, error: null });
    });
  } catch (err) {
    const code = String((err as { message?: string })?.message ?? err).replace(/^.*?([A-Z][A-Z0-9_]{2,})\b.*$/s, "$1");
    toast(MODEL_REFUSED[code] ?? m.name + " GPU 版本操作未完成（" + code + "）");
  }
  await refreshModels();
}

export function prepareModel(id: string | null | undefined = contextModelId(getState())): void {
  const s = getState();
  const m = modelById(s.models, id);
  if (!m) return;
  if (documentBusy(s)) { toast("请先完成或停止当前文档处理，再准备模型"); return; }
  if (getService().engine) { void liveModel(m, (e) => e.prepareModel(m.id), "loading"); return; }
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
  if (getService().engine) { void liveModel(m, (e) => e.pauseModel(m.id), "paused"); return; }
  stopModelTimer(m.id);
  store.update((d) => { modelById(d.models, m.id)!.status = "paused"; });
}

/** A Hugging Face model id as the kernel reports it; anything else is never turned into an address. */
const HUB_REPO = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/;

/** The model's page on Hugging Face, or null for an id that is not a plain "owner/name". */
export const hubUrl = (repo: string): string | null =>
  HUB_REPO.test(repo) && !repo.includes("..") ? "https://huggingface.co/" + repo : null;

/** Opens the model's Hugging Face page in the system browser. */
export function openModelPage(repo: string): void {
  const url = hubUrl(repo);
  if (!url) return;
  getService().openUrl(url).catch(() => toast("无法打开浏览器，模型页地址：" + url));
}

/** Shows the folder of a model's files in Finder / Explorer. */
export async function openModelDirectory(id: string): Promise<void> {
  const engine = getService().engine;
  if (!engine) { toast("请在桌面应用中打开模型目录"); return; }
  try {
    await engine.openModelDirectory(id);
  } catch {
    toast("无法打开模型目录，请稍后重试");
  }
}

/**
 * A long path cut in two: the head may shrink to an ellipsis, the tail always shows. The tail is the last
 * folder when it is short; otherwise it starts after the first separator (_ - . /) of the last 24 characters, or
 * is the last 20 characters, so the start and the end of the path both stay visible.
 */
export function splitPath(p: string): [string, string] {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  let cut = i;
  if (i < 0 || p.length - i > 24) {
    const from = Math.max(0, p.length - 24);
    const b = p.slice(from).search(/[_\-./\\]/);
    cut = b >= 0 && p.length - (from + b + 1) >= 8 ? from + b + 1 : Math.max(0, p.length - 20);
  }
  return [p.slice(0, cut), p.slice(cut)];
}

/** A macOS home folder shown as "~" (the full path stays in the tooltip and in what is copied). */
export const homeRelative = (p: string): string => p.replace(/^\/Users\/[^/]+(?=\/|$)/, "~");

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
