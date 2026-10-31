import type { CSSProperties, InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from "react";
import { forwardRef } from "react";
import { Icon } from "./Icon";

interface FieldProps extends InputHTMLAttributes<HTMLInputElement> {
  icon?: string;
  /** Style of the surrounding label (spacing). */
  labelStyle?: CSSProperties;
}

/** A single-line text input with an optional leading icon. */
export const Field = forwardRef<HTMLInputElement, FieldProps>(function Field({ icon, labelStyle, ...rest }, ref) {
  return (
    <label className="field" style={labelStyle}>
      {icon && <Icon name={icon} size={15} />}
      <input ref={ref} {...rest} />
    </label>
  );
});

/** A multi-line text input. */
export const TextArea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function TextArea({ className, ...rest }, ref) {
  return (
    <label className="field ta">
      <textarea ref={ref} className={className} {...rest} />
    </label>
  );
});

/** A search box used above tables. */
export function SearchBox({ value, onChange, placeholder = "搜索", label }: { value: string; onChange: (v: string) => void; placeholder?: string; label: string }) {
  return (
    <label className="search">
      <Icon name="search" size={15} />
      <input placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} aria-label={label} />
    </label>
  );
}

/** Term/definition pairs in a two-column grid. */
/** Items are `[term, detail]`, with an optional class for the detail (e.g. "mono"). */
export function KeyValue({ items, className }: { items: [ReactNode, ReactNode, string?][]; className?: string }) {
  return (
    <dl className={className ? "kv " + className : "kv"}>
      {items.map(([k, v, c], i) => (
        <KeyValueItem key={i} term={k} detail={v} detailClass={c} />
      ))}
    </dl>
  );
}
function KeyValueItem({ term, detail, detailClass }: { term: ReactNode; detail: ReactNode; detailClass?: string }) {
  return (
    <>
      <dt>{term}</dt>
      <dd className={detailClass}>{detail}</dd>
    </>
  );
}
