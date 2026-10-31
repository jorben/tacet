import type { CSSProperties } from "react";
import type { CategoryId } from "../domain/types";

/**
 * Attributes for the CSS-only tooltip (`[data-tip]`).
 * `align="end"` keeps it inside the window when the element sits at the right edge; `placement="above"` shows it over the element.
 */
export function tip(text: string, opt: { align?: "end"; placement?: "above" } = {}): Record<string, string> {
  return {
    "data-tip": text,
    ...(opt.align === "end" ? { "data-tr": "" } : {}),
    ...(opt.placement === "above" ? { "data-tt": "" } : {}),
  };
}

/** Sets the `--c` custom property that coloured swatches, hits and rows read. */
export const categoryColor = (t: CategoryId): CSSProperties => ({ ["--c" as string]: `var(--t-${t})` });
