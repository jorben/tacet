// Moving a task off the screen into “最近”, and what its entry says there. These change the state only: a task keeps
// its work in the kernel, which goes on with it whether or not it is on screen.
import { current, isDraft, type Draft } from "immer";
import { batchStatus } from "../domain/batch";
import type { Batch, HistoryEntry, Single } from "../domain/types";
import { fmt } from "../domain/format";
import type { AppState } from "../store";
import { isWorking } from "../store/selectors";
import { nextId } from "./timers";

const plain = <T,>(v: T): T => (isDraft(v) ? (current(v as never) as T) : v);

/** What the entry of a single task says. `state` is the kernel's when it told one: a file it finished working on waits
 *  for the person even if the screen had not got to read it. */
export function singleEntryStatus(s: Single, state?: string): Pick<HistoryEntry, "st" | "stl"> {
  if (s.live?.locked) return { st: "warn", stl: "需要密码" };
  const stopped = state === "awaiting_review" || state === "failed" || state === "blocked" || state === "paused" || (state === "ready_to_publish" && s.phase !== "exporting");
  return isWorking(s) && !stopped ? { st: "run", stl: "处理中" } : { st: "warn", stl: "待确认" };
}

/** What the entry of a batch says: one with files still open can be continued, one that ended is a result. */
export function batchEntryStatus(b: Batch): Pick<HistoryEntry, "st" | "stl" | "park"> {
  const [st, stl] = batchStatus(b);
  if (st === "done" || st === "part") return { st, stl, park: undefined };
  return { st: st === "run" && !b.paused ? "run" : "keep", stl: b.paused && st === "run" ? "已暂停 · 可继续" : stl, park: true };
}

/** The single task on screen goes to “最近”; one that is done is listed there already. */
export function shelveSingle(d: Draft<AppState>): void {
  const s = d.single;
  if (!s) return;
  if (s.phase !== "done") {
    d.history.unshift({ id: nextId("h"), k: "file", ext: s.ext, name: s.name, meta: s.pages.length + " 页", ...singleEntryStatus(s as Single), when: "刚刚", saved: plain(s), undo: plain(d.undo), park: true });
  }
  d.single = null;
  d.undo = [];
}

/** The batch on screen goes to “最近”. */
export function shelveLiveBatch(d: Draft<AppState>): void {
  const b = d.batch;
  if (!b) return;
  d.history.unshift({ id: nextId("h"), k: "batch", name: b.title, meta: fmt(b.n) + " 份", ...batchEntryStatus(plain(b) as Batch), when: "刚刚", saved: plain(b) });
  d.batch = null;
}
