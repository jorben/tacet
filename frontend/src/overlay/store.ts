// Transient UI layered over the pages: one popover/menu, one dialog or drawer, one toast.
// Specs are plain data; `dialogs/` and `overlay/*Host` turn them into components, so actions can open overlays
// without importing any UI.
import type { ManualSelection } from "../domain/single";
import type { CategoryId, RuleDraft } from "../domain/types";
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
  /** Shown but cannot be picked now; `s` says why. */
  off?: boolean;
}
export type MenuEntry = MenuItem | { h: string } | "-";

/** A choice of a menu that stays open while several are ticked. */
export interface MultiItem {
  k: string;
  l: string;
  /** Secondary text, right-aligned. */
  s?: string;
}

export type PopContent =
  | { kind: "menu"; items: MenuEntry[]; onPick: (item: MenuItem) => void }
  /** Ticking an item keeps the menu open; what is ticked is handed over once, when the menu closes. */
  | { kind: "multi"; title: string; items: MultiItem[]; selected: string[]; onClose: (selected: string[]) => void }
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
  | { type: "slotConflict"; go: () => void }
  | { type: "switchPreset"; preset: string; go: () => void }
  | { type: "bulkConfirm"; gid: string; k: string }
  | { type: "password"; gid: string; members: number[] | undefined }
  | { type: "exportReady" }
  | { type: "livePassword"; targets: { job: string; name: string }[] }
  | { type: "liveBlocked"; targets: { job: string; name: string; reason: string }[] }
  /**
   * `index` is the rule's index in the rule set (null adds one). A new rule reports `category` unless the person
   * picks another ("other" without one); an existing rule shows its own. `draft` reopens the editor as it was left.
   */
  | { type: "ruleEditor"; rulesetId: string; index: number | null; category?: CategoryId; draft?: RuleDraft }
  /**
   * A change would take a category's detection source away in schemes that handle it (it turns off there), or
   * leave a scheme no rule switched on: they are listed and the change waits for a yes. `back` is the dialog
   * that cancelling returns to.
   */
  | { type: "detectionLoss"; action: string; lost: { preset: string; categories: CategoryId[] }[]; noRules: string[]; go: () => void; back?: ModalSpec }
  /** Deleting a user category: what lets go of it is listed first. `back` is the dialog to return to. */
  | { type: "deleteCategory"; id: CategoryId; back?: ModalSpec }
  | { type: "versions"; kind: "preset" | "ruleset" }
  | { type: "importConflict"; kind: "preset" | "ruleset"; name: string }
  | { type: "installPlugin" }
  | { type: "uninstallPlugin"; id: string }
  | { type: "updateApp" }
  | { type: "aboutLink"; which: "website" | "discord" | "github" }
  | { type: "licenses" }
  | { type: "cleanWorkspace" }
  | { type: "closeWindow" }
  | { type: "presetTest" };

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

/** A multi-select menu that is closed or replaced hands over what is ticked (after it is gone, so that may open a dialog). */
function finish(pop: OverlayState["pop"]): void {
  if (pop?.content.kind === "multi") pop.content.onClose(pop.content.selected);
}

export function openPop(anchor: PopAnchor, content: PopContent, opt: PopOptions = {}): void {
  const was = overlayStore.get().pop;
  set({ pop: { anchor, content, ...opt, uid: ++seq } });
  finish(was);
}
export function closePop(): void {
  const was = overlayStore.get().pop;
  if (!was) return;
  set({ pop: null });
  finish(was);
}

export function openMenu(anchor: PopAnchor, items: MenuEntry[], onPick: (item: MenuItem) => void, opt: PopOptions = {}): void {
  openPop(anchor, { kind: "menu", items, onPick }, opt);
}

/** A menu where several items are ticked; `onClose` gets the ticked keys once, however the menu closes. */
export function openMultiMenu(anchor: PopAnchor, title: string, items: MultiItem[], selected: string[], onClose: (selected: string[]) => void, opt: PopOptions = {}): void {
  openPop(anchor, { kind: "multi", title, items, selected, onClose }, opt);
}

/** Ticks or unticks an item of the open multi-select menu. Nothing is handed over until it closes. */
export function toggleMultiItem(k: string): void {
  const pop = overlayStore.get().pop;
  if (pop?.content.kind !== "multi") return;
  const c = pop.content;
  set({ pop: { ...pop, content: { ...c, selected: c.selected.includes(k) ? c.selected.filter((x) => x !== k) : [...c.selected, k] } } });
}

/** A dialog replaces any popover, drawer and dialog that is open. */
export function openModal(spec: ModalSpec): void {
  const was = overlayStore.get().pop;
  set({ pop: null, drawer: null, modal: { ...spec, uid: ++seq } });
  finish(was);
}
export function openDrawer(spec: DrawerSpec): void {
  const was = overlayStore.get().pop;
  set({ pop: null, modal: null, drawer: { ...spec, uid: ++seq } });
  finish(was);
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
