import { act, render } from "@testing-library/react";
import { vi } from "vitest";
import App from "../App";
import { scene } from "../engine/proto";

/** The engine keeps module-level state and document listeners, so the app is mounted once per test file. */
export function mountApp() {
  render(<App />);
}

export const $ = (s: string): HTMLElement | null => document.querySelector(s);
export const $$ = (s: string): HTMLElement[] => [...document.querySelectorAll<HTMLElement>(s)];
export const win = () => (document.querySelector("#win") as HTMLElement).textContent!.replace(/\s+/g, " ");

export function go(k: string) {
  act(() => scene(k));
}
export function click(sel: string | HTMLElement) {
  const el = typeof sel === "string" ? $(sel) : sel;
  if (!el) throw new Error("not found: " + sel);
  act(() => el.click());
}
export function clickA(a: string, v?: string) {
  click(`[data-a="${a}"]${v != null ? `[data-v="${v}"]` : ""}`);
}
export function tick(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}
export function key(k: string, opt: KeyboardEventInit = {}) {
  act(() => {
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, ...opt }));
  });
}
