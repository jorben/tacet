import { afterEach, beforeEach, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import { stopAllTimers } from "../actions/timers";
import { setContinuation } from "../actions/pending";
import { resetScenes } from "../dev/scenes";
import { resetOverlay } from "../overlay/store";
import { resetStore } from "../store";

// jsdom gaps used by the UI
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false }),
});
Element.prototype.scrollIntoView = vi.fn();
Element.prototype.scrollTo = vi.fn() as any;
window.scrollTo = vi.fn() as any;

// The store, the overlay and the timers are module-level, so every test starts from a fresh installation.
beforeEach(() => {
  stopAllTimers();
  setContinuation(null);
  resetScenes();
  resetOverlay();
  resetStore();
});

afterEach(() => {
  stopAllTimers();
  vi.useRealTimers();
});
