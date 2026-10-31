import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { expect, vi } from "vitest";
import App from "../App";
import { resetScenes, scene } from "../dev/scenes";

/** The store and the document listeners are module-level, so the app is mounted once per test file and `go()` resets the state. */
export async function mountApp() {
  render(<App />);
  // the scene navigator is loaded on demand
  await waitFor(() => expect(document.querySelector("#proto [data-scene]")).toBeTruthy());
}

export const $ = (s: string, root: ParentNode = document): HTMLElement | null => root.querySelector(s);
export const $$ = (s: string, root: ParentNode = document): HTMLElement[] => [...root.querySelectorAll<HTMLElement>(s)];
export const win = () => (document.querySelector("#win") as HTMLElement).textContent!.replace(/\s+/g, " ");
export const text = (s: string) => ($(s)?.textContent ?? "").replace(/\s+/g, " ");

export function go(k: string) {
  act(() => scene(k));
}
export { resetScenes };

/** What a person reads on a control: its accessible name, or else its text. */
const label = (e: HTMLElement) => (e.getAttribute("aria-label") ?? e.textContent ?? "").replace(/\s+/g, " ").trim();

/** Buttons whose label equals `name` (preferred) or contains it, inside `root`. */
export function buttons(name: string, root: ParentNode = document): HTMLElement[] {
  const all = $$("button,[role=button],a[href]", root);
  const exact = all.filter((e) => label(e) === name);
  return exact.length ? exact : all.filter((e) => label(e).includes(name));
}
export function button(name: string, root: ParentNode = document): HTMLElement {
  const b = buttons(name, root)[0];
  if (!b) throw new Error(`no button “${name}” in ${root === document ? "document" : (root as Element).className}`);
  return b;
}

export function click(el: string | Element) {
  const target = (typeof el === "string" ? $(el) : el) as HTMLElement | null;
  if (!target) throw new Error("not found: " + el);
  act(() => target.click());
}
/** Click the button labelled `name`, optionally only inside the first element matching `within`. */
export function press(name: string, within?: string) {
  const root = within ? $(within) : document;
  if (!root) throw new Error("not found: " + within);
  click(button(name, root));
}
/** Pick an entry of the open menu. */
export function pick(name: string) {
  const item = $$("#layer .mi").find((e) => e.textContent!.includes(name));
  if (!item) throw new Error(`no menu item “${name}”`);
  click(item);
}
export const menuTexts = () => $$("#layer .mi").map((e) => e.textContent).join();

export function type(el: string | HTMLElement, value: string) {
  const target = (typeof el === "string" ? $(el) : el) as HTMLInputElement | HTMLTextAreaElement | null;
  if (!target) throw new Error("not found: " + el);
  act(() => { fireEvent.change(target, { target: { value } }); });
}

/** Pick files in the home page's file input. */
export function chooseFiles(...names: string[]) {
  const input = $("#fileIn") as HTMLInputElement;
  act(() => { fireEvent.change(input, { target: { files: names.map((n) => new File(["x"], n)) } }); });
}

export function tick(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}
export function key(k: string, opt: KeyboardEventInit = {}) {
  act(() => {
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...opt }));
  });
}
