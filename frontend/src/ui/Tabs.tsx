import type { ReactNode } from "react";

export interface TabItem<V extends string> {
  value: V;
  label: ReactNode;
}

export function Tabs<V extends string>({ tabs, value, onChange, flush }: { tabs: TabItem<V>[]; value: V; onChange: (v: V) => void; flush?: boolean }) {
  return (
    <div className="tabs" role="tablist" style={flush ? { margin: "0 0 22px" } : undefined}>
      {tabs.map((t) => (
        <button key={t.value} type="button" className="tab" role="tab" aria-selected={value === t.value} onClick={() => onChange(t.value)}>
          {t.label}
        </button>
      ))}
    </div>
  );
}

/** Filter buttons with counts, used above tables. */
export function FilterChips<V extends string>({ options, value, onChange }: { options: { value: V; label: string; count: number }[]; value: V; onChange: (v: V) => void }) {
  return (
    <>
      {options.map((o) => (
        <button key={o.value} type="button" className="fchip" aria-pressed={value === o.value} onClick={() => onChange(o.value)}>
          {o.label} <span className="num">{o.count.toLocaleString("zh-CN")}</span>
        </button>
      ))}
    </>
  );
}
