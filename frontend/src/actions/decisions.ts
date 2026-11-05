// Writing review decisions to the kernel. Every write goes through here, because the order matters and the calls
// do not keep it by themselves: a write can take longer than the next call. Writes of one file land in the order
// they were made, and whatever reads a file or acts on what it holds can wait for them first. A write that failed
// is kept until a later one of that file lands: until then the kernel holds older choices than the person made.
import { getService } from "../services";

export type Decisions = { id: number; keep: boolean }[];

/** The choices of a file that the kernel refused, and why. Each write sends all of them, so one per file is enough. */
export interface UnsavedDecisions { job: string; ds: Decisions; failure: unknown }

/** The last write of each file still on its way. */
const pending = new Map<string, Promise<void>>();
const unsaved = new Map<string, UnsavedDecisions>();

/** Queues a write behind the ones of this file. It is registered at once, so a read that follows can wait for it. */
export function sendDecisions(job: string, ds: Decisions): Promise<void> {
  const before = pending.get(job) ?? Promise.resolve();
  const e = getService().engine;
  if (!e || !ds.length) return before;
  const write = before.then(() => e.decide(job, ds));
  const last: Promise<void> = write.then(
    () => { unsaved.delete(job); },
    (failure: unknown) => { unsaved.set(job, { job, ds, failure }); },
  ).then(() => { if (pending.get(job) === last) pending.delete(job); });
  pending.set(job, last);
  return write;
}

/** Waits for the writes of one file. */
export const decisionsSettled = (job: string): Promise<void> => pending.get(job) ?? Promise.resolve();

/** The choices of a file that have not reached the kernel, as of now: wait for its writes first. */
export const unsavedDecisions = (job: string): Decisions | undefined => unsaved.get(job)?.ds;

/** Waits until no write is on its way, those made during the wait too: a person can go on choosing meanwhile. What
 *  has not reached the kernel by then, including earlier failures. */
export async function allDecisionsSettled(): Promise<UnsavedDecisions[]> {
  while (pending.size) await Promise.all(pending.values());
  return [...unsaved.values()];
}

/** Sends again what the kernel refused; `allDecisionsSettled` tells how that went. What was refused is older than any
 *  write of its file made since, so call this with none on its way, or it lands over the newer one. */
export function resendDecisions(jobs: string[]): void {
  for (const job of jobs) {
    const u = unsaved.get(job);
    if (u) void sendDecisions(job, u.ds).catch(() => {});
  }
}

/** The file will not be exported, so what the kernel refused on it no longer stands in the way. */
export async function forgetDecisions(job: string): Promise<void> {
  await decisionsSettled(job);
  unsaved.delete(job);
}

/** A fresh start (tests): the jobs of the last kernel mean nothing to the next. */
export function resetDecisions(): void {
  pending.clear();
  unsaved.clear();
}
