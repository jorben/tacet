import type { CSSProperties } from "react";
import type { CategoryId } from "../domain/types";

/**
 * Attributes for the CSS-only tooltip (`[data-tip]`).
 * `align="end"` keeps it inside the window when the element sits at the right edge, `align="start"` when it sits at
 * the left edge of a scrolling pane (which clips what sticks out); `placement="above"` shows it over the element;
 * `wrap` lets a long text break into lines instead of running off the window.
 */
export function tip(text: string, opt: { align?: "end" | "start"; placement?: "above"; wrap?: boolean } = {}): Record<string, string> {
  return {
    "data-tip": text,
    ...(opt.align === "end" ? { "data-tr": "" } : opt.align === "start" ? { "data-tl": "" } : {}),
    ...(opt.placement === "above" ? { "data-tt": "" } : {}),
    ...(opt.wrap ? { "data-tw": "" } : {}),
  };
}

/** Sets the `--c` custom property that coloured swatches, hits and rows read. */
export const categoryColor = (t: CategoryId): CSSProperties => ({ ["--c" as string]: `var(--t-${t}, var(--t-other))` });
