import { act, render, waitFor } from "@testing-library/react";
import { expect, vi } from "vitest";
import App from "../App";
import { scene } from "../dev/scenes";

/** The engine keeps module-level state and document listeners, so the app is mounted once per test file. */
export async function mountApp() {
  render(<App />);
  // the scene navigator is loaded on demand
  await waitFor(() => expect(document.querySelector("#proto [data-scene]")).toBeTruthy());
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
