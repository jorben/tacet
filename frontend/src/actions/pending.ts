// An operation that waits for a model to become ready is stored in the state as plain data (`modelPending`);
// the function that continues it lives here so the state stays serialisable.

let continuation: (() => void) | null = null;

export function setContinuation(go: (() => void) | null): void {
  continuation = go;
}

/** Takes the stored continuation; it can only be run once. */
export function takeContinuation(): (() => void) | null {
  const go = continuation;
  continuation = null;
  return go;
}
