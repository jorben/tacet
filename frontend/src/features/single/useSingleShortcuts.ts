import { useEffect } from "react";
import { clearFocus, exportSingle, stepHit, toggleFocusedHit, undoSingle } from "../../actions/single";
import { closePop } from "../../overlay/store";
import { getState } from "../../store";
import type { ViewMode } from "../../domain/types";

/** Keyboard shortcuts of the single-file review (J/K, X, E, C, ⌘Z, ⌘↩, Esc). */
export function useSingleShortcuts(changeMode: (m: ViewMode) => void): void {
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      // Esc closes overlays first (handled by the overlay host, which marks the event).
      if (ev.defaultPrevented) return;
      const s = getState().single;
      if (!s || s.phase !== "review") return;
      if (ev.key === "Escape") { clearFocus(); return; }
      const target = ev.target as HTMLInputElement | null;
      if (target?.tagName === "INPUT" && target.type !== "checkbox") return;
      const k = ev.key.toLowerCase();
      const mod = ev.metaKey || ev.ctrlKey;
      if (mod && k === "z") { ev.preventDefault(); undoSingle(); return; }
      if (mod && ev.key === "Enter") { ev.preventDefault(); if (s.from !== "batch") exportSingle(); return; }
      if (mod) return;
      if (k === "j" || ev.key === "ArrowDown") { ev.preventDefault(); stepHit(1); }
      else if (k === "k" || ev.key === "ArrowUp") { ev.preventDefault(); stepHit(-1); }
      else if (k === "x" && s.focus >= 0) toggleFocusedHit();
      else if (k === "e") { closePop(); changeMode(s.mode === "orig" ? "fx" : "orig"); }
      else if (k === "c") { closePop(); changeMode(s.mode === "cmp" ? "orig" : "cmp"); }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [changeMode]);
}
