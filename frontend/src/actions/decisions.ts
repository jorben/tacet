// Writing review decisions to the kernel. Every write goes through here, because the order matters and the calls
// do not keep it by themselves: a write can take longer than the next call. Writes of one file land in the order
// they were made, and whatever reads a file or acts on what it holds can wait for them first.
import { getService } from "../services";

type Decisions = { id: number; keep: boolean }[];

/** The last write of each file still on its way: it settles with the failure of that write, or undefined. */
const pending = new Map<string, Promise<unknown>>();

/** Queues a write behind the ones of this file. It is registered at once, so a read that follows can wait for it. */
export function sendDecisions(job: string, ds: Decisions): Promise<void> {
  const before = pending.get(job) ?? Promise.resolve();
  const e = getService().engine;
  if (!e || !ds.length) return before.then(() => {});
  const write = before.then(() => e.decide(job, ds));
  const last: Promise<unknown> = write.then(() => undefined, (err: unknown) => err).then((failure) => {
    if (pending.get(job) === last) pending.delete(job);
    return failure;
  });
  pending.set(job, last);
  return write;
}

/** Waits for the writes of one file. Each sends all its choices, so only the last one's failure counts. */
export const decisionsSettled = (job: string): Promise<unknown> => pending.get(job) ?? Promise.resolve();

/** Waits for every write on its way; the first failure among them, if any. */
export async function allDecisionsSettled(): Promise<unknown> {
  return (await Promise.all(pending.values())).find((failure) => failure !== undefined);
}
