// Settings, application updates and workspace cleanup. Updates and cleaning are demonstrations: nothing is downloaded or deleted.
import type { KeepDays, RedactStyle, Settings, ThemePref } from "../domain/types";
import { getState, store } from "../store";
import { closeModal, openModal, toast, type PopAnchor } from "../overlay/store";
import { openKeepMenu } from "./batch";
import { stopBatchTimer, stopSingleTimer } from "./timers";
import { slotBusy } from "../store/selectors";
import { saveSettingsSoon } from "./liveConfig";
import { isLive, liveEngine } from "./live";

const systemTheme = (): "light" | "dark" => (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");

export function setThemePref(pref: ThemePref): void {
  store.update((d) => { d.themePref = pref; d.theme = pref === "system" ? systemTheme() : pref; });
  saveSettingsSoon();
}

export function setSetting<K extends keyof Settings>(key: K, value: Settings[K]): void {
  store.update((d) => { d.settings[key] = value; });
  saveSettingsSoon();
}

export const setRedactStyle = (v: RedactStyle): void => setSetting("redact", v);

export function openSettingsKeepMenu(anchor: PopAnchor): void {
  openKeepMenu(anchor, getState().settings.keep, (k: KeepDays) => setSetting("keep", k));
}

/* ── Updates ──────────────────────────────────────────────────────────────── */

export function checkForUpdate(): void {
  if (isLive()) { toast("更新通道尚未配置，当前版本无法检查更新"); return; }
  if (["checking", "updating"].includes(getState().update.status)) return;
  store.update((d) => { d.update.status = "checking"; });
  setTimeout(() => {
    store.update((d) => {
      const u = d.update;
      u.latest = u.current === "1.0.1" ? null : "1.0.1";
      u.status = u.latest ? "available" : "latest";
    });
    const latest = getState().update.latest;
    toast(latest ? "发现新版本 " + latest : "已是最新版本");
  }, 900);
}

export function startUpdate(): void {
  if (isLive()) return;
  if (getState().update.status !== "available") return;
  openModal({ type: "updateApp" });
}

/** `unfinished` says what happens to tasks that are not done: drop them, or run their pending steps again afterwards. */
export function confirmUpdate(unfinished?: "drop" | "rerun"): void {
  if (getState().update.status !== "available") return;
  closeModal();
  if (unfinished === "drop") {
    const s = getState();
    if (slotBusy(s, "single")) { stopSingleTimer(); store.update((d) => { d.single = null; d.undo = []; }); }
    if (slotBusy(s, "batch")) { stopBatchTimer(); store.update((d) => { d.batch = null; }); }
    store.update((d) => { d.history = d.history.filter((h) => !(h.park || h.st === "keep")); });
  }
  store.update((d) => { d.update.status = "updating"; });
  setTimeout(() => {
    store.update((d) => { d.update.current = d.update.latest!; d.update.latest = null; d.update.status = "latest"; });
    toast("更新完成" + (unfinished === "drop" ? " · 已作废未完成的任务" : unfinished === "rerun" ? " · 未完成的任务将按新版本重新执行" : "") + " · 更新功能尚未接入");
  }, 1200);
}

/* ── Workspace ────────────────────────────────────────────────────────────── */

export async function openWorkspaceDirectory(): Promise<void> {
  const engine = liveEngine();
  if (!engine) { toast("请在桌面应用中打开工作区目录"); return; }
  try {
    await engine.openWorkspace();
  } catch {
    toast("无法打开工作区目录，请稍后重试");
  }
}

export function cleanWorkspace(): void {
  closeModal();
  store.update((d) => { d.history = d.history.filter((h) => h.st !== "keep"); });
  toast("已清理 · 1.4 GiB");
}
