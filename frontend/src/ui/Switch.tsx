interface SwitchProps {
  checked: boolean;
  onChange: () => void;
  /** Accessible name (the switch has no visible label of its own). */
  label: string;
  disabled?: boolean;
  id?: string;
}

export function Switch({ checked, onChange, label, disabled, id }: SwitchProps) {
  return <button type="button" id={id} className="tg" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={onChange} />;
}
