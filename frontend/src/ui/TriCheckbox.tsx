import type { ButtonHTMLAttributes } from "react";

/** A derived group state; clicking a mixed group enables every item. */
export function TriCheckbox({ checked, label, onChange, ...rest }: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onChange"> & { checked: boolean | "mixed"; label: string; onChange: () => void }) {
  return <button {...rest} type="button" className="tri-checkbox" role="checkbox" aria-checked={checked} aria-label={label} onClick={onChange}>{checked === "mixed" ? "−" : checked ? "✓" : ""}</button>;
}
