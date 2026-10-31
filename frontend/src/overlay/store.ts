// Transient UI layered over the pages: one popover/menu, one dialog or drawer, one toast.
// Specs are plain data; `dialogs/` and `overlay/*Host` turn them into components, so actions can open overlays
// without importing any UI.
import type { ManualSelection } from "../domain/single";
import type { CategoryId } from "../domain/types";
import { createStore, useStoreSelector } from "../store/createStore";

/* ── Popovers and menus ───────────────────────────────────────────────────── */

export type PopAnchor = HTMLElement | DOMRect | (() => HTMLElement | DOMRect | null);

export interface MenuItem {
  l: string;
  /** Secondary text, right-aligned. */
  s?: string;
  ic?: string;
  k: string | number;
  /** Destructive. */
  d?: boolean;
  /** Currently selected. */
  on?: boolean;
}
export type MenuEntry = MenuItem | { h: string } | "-";

export type PopContent =
  | { kind: "menu"; items: MenuEntry[]; onPick: (item: MenuItem) => void }
  | { kind: "hit"; hitId: number }
  | { kind: "keys" }
  | { kind: "selection"; selection: ManualSelection }
  | { kind: "badFiles" };

export interface PopOptions {
  alignRight?: boolean;
  role?: "menu" | "dialog";
  /** Move focus into the popover (default true). */
  focus?: boolean;
}
export interface PopSpec extends PopOptions {
  anchor: PopAnchor;
  content: PopContent;
}

/* ── Dialogs and drawers ──────────────────────────────────────────────────── */

export interface SampleRow {
  fileIndex: number;
  fileName: string;
  location: string;
  value: string;
}

export type ModalSpec =
  | { type: "recent" }
  | { type: "historyItem"; entryId: string }
  | { type: "resumeBatch"; entryId: string }
  | { type: "slotConflict"; kind: "single" | "batch"; go: () => void }
  | { type: "switchPreset"; preset: string; go: () => void }
  | { type: "bulkConfirm"; gid: string; k: string }
  | { type: "password"; gid: string; members: number[] | undefined }
  | { type: "exportReady" }
  | { type: "ruleEditor"; rulesetId: string; index: number | null }
  | { type: "versions"; kind: "preset" | "ruleset" }
  | { type: "importConflict"; kind: "preset" | "ruleset"; name: string }
  | { type: "installPlugin" }
  | { type: "uninstallPlugin"; id: string }
  | { type: "updateApp" }
  | { type: "aboutLink"; which: "website" | "discord" | "github" }
  | { type: "licenses" }
  | { type: "cleanWorkspace" }
  | { type: "closeWindow" };

export type DrawerSpec = { type: "sample"; category: CategoryId | "img"; rows: SampleRow[] };

export interface ToastSpec {
  id: number;
  msg: string;
  undo?: () => void;
}

export interface OverlayState {
  pop: (PopSpec & { uid: number }) | null;
  modal: (ModalSpec & { uid: number }) | null;
  drawer: (DrawerSpec & { uid: number }) | null;
  toast: ToastSpec | null;
}

const empty = (): OverlayState => ({ pop: null, modal: null, drawer: null, toast: null });
export const overlayStore = createStore<OverlayState>(empty());

export const useOverlay = <U,>(selector: (s: OverlayState) => U): U => useStoreSelector(overlayStore, selector);

let seq = 0;
const set = (patch: Partial<OverlayState>) => overlayStore.replace({ ...overlayStore.get(), ...patch });

export function openPop(anchor: PopAnchor, content: PopContent, opt: PopOptions = {}): void {
  set({ pop: { anchor, content, ...opt, uid: ++seq } });
}
export function closePop(): void {
  if (overlayStore.get().pop) set({ pop: null });
}

export function openMenu(anchor: PopAnchor, items: MenuEntry[], onPick: (item: MenuItem) => void, opt: PopOptions = {}): void {
  openPop(anchor, { kind: "menu", items, onPick }, opt);
}

/** A dialog replaces any popover, drawer and dialog that is open. */
export function openModal(spec: ModalSpec): void {
  set({ pop: null, drawer: null, modal: { ...spec, uid: ++seq } });
}
export function openDrawer(spec: DrawerSpec): void {
  set({ pop: null, modal: null, drawer: { ...spec, uid: ++seq } });
}
export function closeModal(): void {
  const s = overlayStore.get();
  if (s.modal || s.drawer) set({ modal: null, drawer: null });
}
export const hasDialog = (): boolean => !!overlayStore.get().modal;

let toastTimer: ReturnType<typeof setTimeout> | undefined;
export function toast(msg: string, undo?: () => void): void {
  clearTimeout(toastTimer);
  const id = ++seq;
  set({ toast: { id, msg, undo } });
  toastTimer = setTimeout(() => {
    if (overlayStore.get().toast?.id === id) set({ toast: null });
  }, undo ? 5000 : 2600);
}
export function dismissToast(): void {
  clearTimeout(toastTimer);
  if (overlayStore.get().toast) set({ toast: null });
}

/** Clears everything (tests and scene changes). */
export function resetOverlay(): void {
  clearTimeout(toastTimer);
  overlayStore.replace(empty());
}
