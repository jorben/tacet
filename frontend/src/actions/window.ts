// The application window: minimising and closing, including the guard for unfinished work.
import { exportedCount, countFiles } from "../domain/batch";
import { fmt } from "../domain/format";
import { getService } from "../services";
import { getState, store } from "../store";
import { refusedFiles, unfinishedBatch, unfinishedOnScreen } from "../store/selectors";
import { closeModal, openModal, toast } from "../overlay/store";
import { stopBatchTimer, stopSingleTimer } from "./timers";
import { leaveBatchFile } from "./batch";
import { allDecisionsSettled, refusedJobs } from "./decisions";
import { discardLiveTask, discardRefused, openDecisions, saveOpenDecisions, savedBatchId } from "./live";

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
  if (!unfinishedOnScreen(s, "single") && !unfinishedBatch(s) && !s.history.some((h) => h.park)) { quitApp(); return; }
  openModal({ type: "closeWindow" });
}

/** Keep unfinished work (paused, encrypted) and quit. Choices only this window holds go to the kernel first. */
export function saveAndQuit(): void {
  closeModal();
  void saveOpenDecisions(openDecisions(getState())).then((saved) => {
    if (!saved) return; // the window stays, and nothing was paused: the batch goes on as the screen says
    markKeptPaused();
    quitApp();
  });
}

const markKeptPaused = () => store.update((d) => { if (d.batch) d.batch.paused = true; });
const refusedJobsKept = () => refusedFiles(getState(), refusedJobs()).length > 0;

/** “停止并清理”, then quit; exported files are not affected. Where the kernel refused choices, what goes is the files
 *  they were on, as the dialog said; with none refused, everything it listed. A write still on its way decides between
 *  the two when it ends, and goes the safe way: refused, it costs the file and not the rest. */
export function stopAndQuit(): void {
  closeModal();
  const named = refusedFiles(getState(), refusedJobs());
  store.update((d) => {
    if (d.single && d.single.from === "batch") leaveBatchFile(d);
  });
  void allDecisionsSettled().then(async () => {
    if (!named.length && !refusedJobsKept()) { discardEverything(); return; }
    await discardRefused(named);
    markKeptPaused();
    quitApp();
  });
}

function discardEverything(): void {
  // The kernel would list them all again at the next start: what was listed in the dialog is stopped there too.
  const s = getState();
  const kept = [savedBatchId(unfinishedOnScreen(s, "single") ?? undefined), savedBatchId(unfinishedBatch(s) ?? undefined),
    ...s.history.filter((h) => h.park).map((h) => savedBatchId(h.saved))].filter((id): id is string => !!id);
  if (unfinishedOnScreen(s, "single")) { stopSingleTimer(); store.update((d) => { d.single = null; d.undo = []; }); }
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
