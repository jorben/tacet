import { vi } from "vitest";
import "@testing-library/jest-dom/vitest";

// jsdom gaps used by the UI engine
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false }),
});
Element.prototype.scrollIntoView = vi.fn();
Element.prototype.scrollTo = vi.fn() as any;
window.scrollTo = vi.fn() as any;
