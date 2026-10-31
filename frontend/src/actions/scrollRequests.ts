// A page that has just been opened sometimes has to scroll somewhere (e.g. to a model card). The action asks,
// the page does it once it is on screen.
import { useEffect } from "react";

export type ScrollRequest = { kind: "models" } | { kind: "model"; id: string };

let pending: ScrollRequest | null = null;
const listeners = new Set<() => void>();

export function requestScroll(r: ScrollRequest): void {
  pending = r;
  listeners.forEach((l) => l());
}

/** Runs `handle` for a request made before the component mounted, and for every later one while it is mounted. */
export function useScrollRequests(handle: (r: ScrollRequest) => void): void {
  useEffect(() => {
    const run = () => {
      const r = pending;
      if (!r) return;
      pending = null;
      handle(r);
    };
    run();
    listeners.add(run);
    return () => {
      listeners.delete(run);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
