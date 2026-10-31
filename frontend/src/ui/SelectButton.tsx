import type { ButtonHTMLAttributes, ReactNode } from "react";
import { Icon } from "./Icon";

interface SelectButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  children: ReactNode;
  /** Smaller variant used in dense lists. */
  small?: boolean;
  /** Show the dropdown chevron (default true). */
  chevron?: boolean;
}

/** The button that opens a menu of choices; it shows the current choice. */
export function SelectButton({ children, small, chevron = true, className, type = "button", ...rest }: SelectButtonProps) {
  return (
    <button type={type} className={["sel", small && "sm", className].filter(Boolean).join(" ")} {...rest}>
      {children}
      {chevron && <Icon name="chev-d" size={small ? 12 : 14} />}
    </button>
  );
}
