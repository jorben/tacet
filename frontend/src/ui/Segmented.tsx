import type { CSSProperties, ReactNode } from "react";
import { Icon } from "./Icon";
import { tip } from "./tip";

export interface SegmentedOption<V extends string> {
  value: V;
  label: ReactNode;
  icon?: string;
  tooltip?: string;
  /** Tooltip shifted to the right edge (for options at the end of a toolbar). */
  tooltipAlign?: "end";
  disabled?: boolean;
}

interface SegmentedProps<V extends string> {
  options: SegmentedOption<V>[];
  value: V;
  onChange: (value: V) => void;
  /** Names the group for assistive technology. */
  label?: string;
  /** Stretch the options over the full width. */
  full?: boolean;
  disabled?: boolean;
  style?: CSSProperties;
}

/** A group of mutually exclusive choices shown as a segmented control. */
export function Segmented<V extends string>({ options, value, onChange, label, full, disabled, style }: SegmentedProps<V>) {
  return (
    <div className={full ? "seg full" : "seg"} role={label ? "group" : undefined} aria-label={label} style={style}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={value === o.value}
          disabled={disabled || o.disabled}
          onClick={() => onChange(o.value)}
          {...(o.tooltip ? tip(o.tooltip, { align: o.tooltipAlign }) : {})}
        >
          {o.icon && <Icon name={o.icon} size={15} />}
          {o.label}
        </button>
      ))}
    </div>
  );
}
