import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";
import type { PopAnchor, PopSpec } from "./store";

function resolve(anchor: PopAnchor): HTMLElement | DOMRect | null {
  return typeof anchor === "function" ? anchor() : anchor;
}

const EDGE = 8;
const GAP = 6;

/**
 * Places the popover below its anchor, above when it fits only there, and otherwise on the side with more room with
 * its height capped so it scrolls inside the layer instead of running off it (an anchor near or past the bottom).
 */
export function place(el: HTMLElement, spec: Pick<PopSpec, "anchor" | "alignRight">): void {
  const layer = el.parentElement;
  const anchor = resolve(spec.anchor);
  if (!layer || !anchor) return;
  const w = layer.getBoundingClientRect();
  const r = anchor instanceof Element ? anchor.getBoundingClientRect() : anchor;
  // the natural height, not one an earlier placement capped
  el.style.maxHeight = "";
  el.style.overflowY = "";
  const pw = el.offsetWidth;
  const ph = el.offsetHeight;
  let x = spec.alignRight ? r.right - w.left - pw : r.left - w.left;
  x = Math.max(EDGE, Math.min(x, w.width - pw - EDGE));
  const top = Math.min(Math.max(r.top - w.top, EDGE), w.height - EDGE);
  const bottom = Math.min(Math.max(r.bottom - w.top, EDGE), w.height - EDGE);
  const below = w.height - EDGE - (bottom + GAP);
  const above = top - GAP - EDGE;
  let y: number;
  if (ph <= below) y = bottom + GAP;
  else if (ph <= above) y = top - GAP - ph;
  else {
    const room = Math.max(below, above);
    y = below >= above ? bottom + GAP : top - GAP - room;
    el.style.maxHeight = Math.max(room, 0) + "px";
    el.style.overflowY = "auto";
  }
  el.style.left = x + "px";
  el.style.top = y + "px";
}

interface PopoverProps {
  spec: PopSpec & { uid: number };
  children: ReactNode;
}

/** A floating panel attached to an element. It repositions when its content changes size. */
export function Popover({ spec, children }: PopoverProps) {
  const ref = useRef<HTMLDivElement>(null);
  const specRef = useRef(spec);
  specRef.current = spec;

  useLayoutEffect(() => {
    const el = ref.current!;
    place(el, spec);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => place(el, specRef.current));
    ro.observe(el);
    return () => ro.disconnect();
  }, [spec.uid]);

  useEffect(() => {
    if (spec.focus === false) return;
    ref.current?.querySelector<HTMLElement>("button,input")?.focus({ preventScroll: true });
  }, [spec.uid, spec.focus]);

  return (
    <div ref={ref} className="pop" role={spec.role || "menu"}>
      {children}
    </div>
  );
}
