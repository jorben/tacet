// The application window: minimising and closing, including the guard for unfinished work.
import { exportedCount, countFiles } from "../domain/batch";
import { fmt } from "../domain/format";
import { getService } from "../services";
import { getState, store } from "../store";
import { slotBusy, unfinishedBatch } from "../store/selectors";
import { closeModal, openModal, toast } from "../overlay/store";
import { stopBatchTimer, stopSingleTimer } from "./timers";
import { leaveBatchFile } from "./batch";
import { discardLiveTask, savedBatchId } from "./live";

export function minimiseWindow(): void {
  void getService().minimiseWindow().catch(() => {});
}

/** In a browser there is no window to close; say so instead of silently doing nothing. */
export function quitApp(): void {
  const svc = getService();
  if (svc.host === "browser") {
    toast("浏览器预览：无法关闭窗口");
    return;
  }
  void svc.quitApp().catch(() => {});
}

/** The user asked to close the window (title-bar button, Cmd+W). */
export function requestClose(): void {
  const s = getState();
  if (s.settings.closeAct === "tray") {
    // a browser has no tray: there the window is only minimised
    if (getService().host === "browser") minimiseWindow(); else void getService().hideWindow().catch(() => {});
    toast("已最小化到托盘" + (s.batch || s.single ? "，任务继续" : ""));
    return;
  }
  requestQuit();
}

/** An explicit quit (the window in “关闭程序” mode, or “退出” in the tray): unfinished work is asked about once. */
export function requestQuit(): void {
  const s = getState();
  if (!slotBusy(s, "single") && !unfinishedBatch(s) && !s.history.some((h) => h.park)) { quitApp(); return; }
  openModal({ type: "closeWindow" });
}

/** Keep unfinished work (paused, encrypted) and quit. */
export function saveAndQuit(): void {
  closeModal();
  store.update((d) => { if (d.batch) d.batch.paused = true; });
  quitApp();
}

/** Discard unfinished work and quit; exported files are not affected. */
export function stopAndQuit(): void {
  closeModal();
  // The kernel would list them all again at the next start: what was listed in the dialog is stopped there too.
  const before = getState();
  const kept = [savedBatchId(slotBusy(before, "single") ?? undefined), savedBatchId(unfinishedBatch(before) ?? undefined),
    ...before.history.filter((h) => h.park).map((h) => savedBatchId(h.saved))].filter((id): id is string => !!id);
  store.update((d) => {
    if (d.single && d.single.from === "batch") leaveBatchFile(d);
  });
  const s = getState();
  if (slotBusy(s, "single")) { stopSingleTimer(); store.update((d) => { d.single = null; d.undo = []; }); }
  const bb = unfinishedBatch(s);
  if (bb) {
    stopBatchTimer();
    store.update((d) => {
      if (bb.phase !== "precheck") {
        d.history.unshift({
          id: "stopped-" + Date.now(), k: "batch", name: bb.title, meta: fmt(bb.n) + " 份", st: "stop",
          stl: "已停止 · 已导出 " + fmt(exportedCount(countFiles(d.batch!.files))) + " / " + fmt(bb.n), when: "刚刚",
        });
      }
      d.batch = null;
    });
  }
  store.update((d) => { d.history = d.history.filter((h) => !h.park); d.view = "home"; });
  void Promise.all([...new Set(kept)].map(discardLiveTask)).finally(quitApp);
}
