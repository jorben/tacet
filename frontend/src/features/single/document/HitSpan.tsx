import { memo } from "react";
import { clickHit } from "../../../actions/single";
import { CATEGORIES } from "../../../domain/catalog";
import type { Hit, Treatment } from "../../../domain/types";
import { categoryColor } from "../../../ui";
import { useTwin } from "./twin";

interface HitSpanProps {
  hit: Hit;
  treatment: Treatment;
  focused: boolean;
  /** Show the result of the decision (blocked-out or placeholder) instead of the original text. */
  revealed: boolean;
  /** Part of the effect column of the compare view. */
  effectColumn: boolean;
  /** Only one of the two copies of a hit may carry the DOM id. */
  withId: boolean;
}

/** A detected piece of text inside the document. Clicking opens its popover. */
export const HitSpan = memo(function HitSpan({ hit, treatment, focused, revealed, effectColumn, withId }: HitSpanProps) {
  const twin = useTwin(hit.id);
  const placeholder = treatment === "ph";
  const text = revealed && hit.state === "redact" && placeholder ? "［" + CATEGORIES[hit.type].ph + "］" : hit.text;
  const cls = ["hit", hit.state === "keep" && "keep", hit.low && !hit.seen && hit.state === "redact" && "low", placeholder && "ph", focused && "focus", twin && "twin"]
    .filter(Boolean)
    .join(" ");
  return (
    <span
      className={cls}
      style={categoryColor(hit.type)}
      data-hit={hit.id}
      id={withId ? "h" + hit.id : undefined}
      onClick={(e) => {
        e.stopPropagation();
        clickHit(hit.id, effectColumn);
      }}
    >
      {text}
    </span>
  );
});
