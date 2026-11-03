import { afterAll } from "vitest";

// The Wails runtime polls for its environment with window.setInterval for up to five seconds after it is imported.
// A test file that ends sooner would see that timer fire after jsdom was torn down ("window is not defined"), so
// the intervals started while a file runs are cleared when it ends.
//
// This is its own setup file, listed first and importing nothing that reaches the runtime: setup.ts imports the
// runtime through the services, and its imports are evaluated before its body, so a guard installed there came
// after the runtime's poll had started and never cleared it (#16).
const started = new Map<number, string>(); // id -> stack of the call that started it
const realSetInterval = window.setInterval.bind(window);
window.setInterval = ((...args: Parameters<typeof window.setInterval>) => {
  const id = realSetInterval(...args);
  started.set(id, new Error().stack ?? "");
  return id;
}) as typeof window.setInterval;
afterAll(() => {
  started.forEach((_, id) => window.clearInterval(id));
  started.clear();
});

/** How many of the intervals to be cleared when this file ends were started from code whose path contains from. */
export function intervalsStartedBy(from: string): number {
  let n = 0;
  started.forEach((stack) => {
    if (stack.includes(from)) n++;
  });
  return n;
}
