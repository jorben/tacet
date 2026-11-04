// Moving a task off the screen into “最近”. These change the state only: what the kernel does about it is decided
// by the caller (it parks a task that waits for a person when the next import asks it to).
import { current, isDraft, type Draft } from "immer";
import { batchStatus } from "../domain/batch";
import type { Batch } from "../domain/types";
import { fmt } from "../domain/format";
import type { AppState } from "../store";
import { nextId } from "./timers";

const plain = <T,>(v: T): T => (isDraft(v) ? (current(v as never) as T) : v);

/** The single task on screen goes to “最近”; one that is done is listed there already. */
export function shelveSingle(d: Draft<AppState>): void {
  const s = d.single;
  if (!s) return;
  if (s.phase !== "done") {
    d.history.unshift({ id: nextId("h"), k: "file", ext: s.ext, name: s.name, meta: s.pages.length + " 页", st: "warn", stl: "待确认", when: "刚刚", saved: plain(s), undo: plain(d.undo), park: true });
  }
  d.single = null;
  d.undo = [];
}

/** The live batch on screen goes to “最近”: one with files still open can be continued, one that ended is a result. */
export function shelveLiveBatch(d: Draft<AppState>): void {
  const b = d.batch;
  if (!b) return;
  const [st, stl] = batchStatus(plain(b) as Batch);
  const ended = st === "done" || st === "part";
  d.history.unshift({
    id: nextId("h"), k: "batch", name: b.title, meta: fmt(b.n) + " 份", st: ended ? st : "keep", stl, when: "刚刚", saved: plain(b), ...(ended ? {} : { park: true }),
  });
  d.batch = null;
}
