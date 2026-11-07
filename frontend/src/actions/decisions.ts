// Writing review decisions to the kernel. Every write goes through here, because the order matters and the calls
// do not keep it by themselves: a write can take longer than the next call. Writes of one file land in the order
// they were made, and whatever reads a file or acts on what it holds can wait for them first. A write that failed
// is kept until a later one of that file lands: until then the kernel holds older choices than the person made.
// A write refused because the file's document has gone out is another matter: nothing can make it land while that
// holds, so it stands in nobody's way. It is kept to tell the person why, and because that can stop holding: when a
// publish gives up on the file (`reopenDecisions`), it is a failed write like any other, and goes on from the choices it has.
import { getService } from "../services";

export type Decisions = { id: number; keep: boolean }[];

/** The choices of a file that the kernel refused, and why. Each write sends all of them, so one per file is enough. */
export interface UnsavedDecisions { job: string; ds: Decisions; failure: unknown }

/** The kernel will not take these whatever is tried: the document went out from the choices it has. */
const tooLate = (u: UnsavedDecisions): boolean => String((u.failure as { message?: string })?.message ?? u.failure).includes("DECISIONS_CLOSED");

/** What a refusal as too late becomes when the file came back: `errorText` has the sentence. */
const REOPENED = "DECISIONS_REOPENED";

/** The last write of each file still on its way. */
const pending = new Map<string, Promise<void>>();
const unsaved = new Map<string, UnsavedDecisions>();

/** What the kernel refused and could still take: this is what holds a person back, not one that came too late. */
const landable = (): UnsavedDecisions[] => [...unsaved.values()].filter((u) => !tooLate(u));

/** The files of `landable`, for a screen that says what a failure costs: it has to follow them while it is open. One
 *  refused as too late costs nothing, so it must not narrow “停止并清理” to itself and stop a file going out. */
let refused: readonly string[] = [];
const refusedListeners = new Set<() => void>();
function refusedChanged(): void {
  refused = landable().map((u) => u.job);
  refusedListeners.forEach((l) => l());
}
export const refusedJobs = (): readonly string[] => refused;
export function onRefusedChange(listener: () => void): () => void {
  refusedListeners.add(listener);
  return () => refusedListeners.delete(listener);
}

/** Queues a write behind the ones of this file. It is registered at once, so a read that follows can wait for it. */
export function sendDecisions(job: string, ds: Decisions): Promise<void> {
  const before = pending.get(job) ?? Promise.resolve();
  const e = getService().engine;
  if (!e || !ds.length) return before;
  const write = before.then(() => e.decide(job, ds));
  const last: Promise<void> = write.then(
    () => { if (unsaved.delete(job)) refusedChanged(); },
    (failure: unknown) => { unsaved.set(job, { job, ds, failure }); refusedChanged(); },
  ).then(() => { if (pending.get(job) === last) pending.delete(job); });
  pending.set(job, last);
  return write;
}

/** Some write has not been answered: nobody knows yet whether the kernel holds the person's choice. */
export const decisionsOnTheirWay = (): boolean => pending.size > 0;

/** Waits for the writes of one file. */
export const decisionsSettled = (job: string): Promise<void> => pending.get(job) ?? Promise.resolve();

/** The choices of a file that have not reached the kernel, as of now: wait for its writes first. */
export const unsavedDecisions = (job: string): Decisions | undefined => {
  const u = unsaved.get(job);
  return u && !tooLate(u) ? u.ds : undefined;
};

/** The kernel's last refusal of a file, of whatever kind. */
export const refusalOf = (job: string): unknown => unsaved.get(job)?.failure;

/** The files whose last write was refused as too late. */
export const tooLateJobs = (): string[] => [...unsaved.values()].filter(tooLate).map((u) => u.job);

/** The file stands where choices are taken again. What came too late for the publish that failed is what the person
 *  chose, so the next one must not go out without it. */
export function reopenDecisions(job: string): void {
  const u = unsaved.get(job);
  if (!u || !tooLate(u)) return;
  unsaved.set(job, { ...u, failure: new Error(REOPENED) });
  refusedChanged();
}

/** Waits until no write is on its way, those made during the wait too: a person can go on choosing meanwhile. What
 *  has not reached the kernel by then and still can, including earlier failures. */
export async function allDecisionsSettled(): Promise<UnsavedDecisions[]> {
  while (pending.size) await Promise.all(pending.values());
  return landable();
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
  if (unsaved.delete(job)) refusedChanged();
}

/** A fresh start (tests): the jobs of the last kernel mean nothing to the next. */
export function resetDecisions(): void {
  pending.clear();
  unsaved.clear();
  refusedChanged();
}
