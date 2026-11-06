import { openPop } from "../../overlay/store";
import { getState } from "../../store";

/** After the mouse is released inside the document: if plain text of one text run is selected, offer to redact it. */
export function setSelectionPopover(): void {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || getState().single?.live?.closed) return;
  const r = sel.getRangeAt(0);
  const a = r.startContainer;
  const b = r.endContainer;
  if (a !== b || a.nodeType !== 3) return;
  const span = a.parentElement;
  if (!span || !span.classList.contains("tx")) return;
  const st = Math.min(r.startOffset, r.endOffset);
  const en = Math.max(r.startOffset, r.endOffset);
  const text = (a as Text).data.slice(st, en);
  if (!text.trim()) return;
  openPop(r.getBoundingClientRect(), { kind: "selection", selection: { k: span.dataset.k!, st, en, text } }, { focus: false });
}
