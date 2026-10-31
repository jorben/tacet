import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";
import type { PopAnchor, PopSpec } from "./store";

function resolve(anchor: PopAnchor): HTMLElement | DOMRect | null {
  return typeof anchor === "function" ? anchor() : anchor;
}

/** Places the popover below its anchor (above when there is no room), inside the layer. */
function place(el: HTMLElement, spec: Pick<PopSpec, "anchor" | "alignRight">): void {
  const layer = el.parentElement;
  const anchor = resolve(spec.anchor);
  if (!layer || !anchor) return;
  const w = layer.getBoundingClientRect();
  const r = anchor instanceof Element ? anchor.getBoundingClientRect() : anchor;
  const pw = el.offsetWidth;
  const ph = el.offsetHeight;
  let x = spec.alignRight ? r.right - w.left - pw : r.left - w.left;
  let y = r.bottom - w.top + 6;
  if (y + ph > w.height - 8) y = r.top - w.top - ph - 6;
  x = Math.max(8, Math.min(x, w.width - pw - 8));
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
