// Handles of the simulated background work. They are not part of the application state: timers are
// resources of the running page, and state is only ever changed from inside the callbacks.

export const timers: {
  /** Detection and export progress of the single-file task. */
  single: ReturnType<typeof setInterval> | undefined;
  /** The batch clock. */
  batch: ReturnType<typeof setInterval> | undefined;
  /** Model download/preparation, one timer per model. */
  models: Map<string, ReturnType<typeof setInterval>>;
} = { single: undefined, batch: undefined, models: new Map() };

export function stopSingleTimer(): void {
  clearInterval(timers.single);
  timers.single = undefined;
}

export function stopBatchTimer(): void {
  clearInterval(timers.batch);
  timers.batch = undefined;
}

export function stopModelTimer(id: string): void {
  clearInterval(timers.models.get(id));
  timers.models.delete(id);
}

export function stopAllModelTimers(): void {
  timers.models.forEach((t) => clearInterval(t));
  timers.models.clear();
}

export function stopAllTimers(): void {
  stopSingleTimer();
  stopBatchTimer();
  stopAllModelTimers();
}

let runCounter = 0;
/** A fresh identity for a processing or export run. */
export const nextRunId = (): number => ++runCounter;

let idCounter = 0;
export const nextId = (prefix: string): string => `${prefix}${Date.now().toString(36)}${(++idCounter).toString(36)}`;
