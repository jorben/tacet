import type { ButtonHTMLAttributes, HTMLAttributes, ReactNode } from "react";
import type { CategoryId } from "../domain/types";
import { categoryColor } from "./tip";

export type PillTone = "ok" | "warn" | "bad" | "pri" | "ghost";

interface PillProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: PillTone;
  /** A pulsing dot before the text, for work in progress. */
  dot?: boolean;
}

/** A small rounded status label. */
export function Pill({ tone, dot, className, children, ...rest }: PillProps) {
  return (
    <span className={["pill", tone, className].filter(Boolean).join(" ")} {...rest}>
      {dot && <span className="dot" />}
      {children}
    </span>
  );
}

/** A pill that is a button (e.g. “无法处理 3”, community links). */
export function PillButton({ tone, className, children, type = "button", ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: PillTone }) {
  return (
    <button type={type} className={["pill", tone, className].filter(Boolean).join(" ")} {...rest}>
      {children}
    </button>
  );
}

/** A tiny label next to a name (kind of rule, version). */
export function Tag({ primary, children, ...rest }: HTMLAttributes<HTMLSpanElement> & { primary?: boolean; children: ReactNode }) {
  return (
    <span className={primary ? "tag tp" : "tag"} {...rest}>
      {children}
    </span>
  );
}

/** The coloured square that identifies a category. */
export function Swatch({ category, size }: { category: CategoryId; size?: number }) {
  return <span className="sw" style={{ ...categoryColor(category), ...(size ? { width: size, height: size } : {}) }} />;
}
