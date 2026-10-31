import { forwardRef, type AnchorHTMLAttributes, type ButtonHTMLAttributes, type CSSProperties, type ReactNode } from "react";
import { Icon } from "./Icon";
import { tip } from "./tip";

/** Pushes a button to the far left of a flex row (the destructive choice in a dialog footer). */
export const pushLeft: CSSProperties = { marginRight: "auto" };

type Variant = "primary" | "secondary" | "ghost" | "danger";
const VARIANT: Record<Variant, string> = { primary: "btn-p", secondary: "btn-s", ghost: "btn-g", danger: "btn-d" };

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  large?: boolean;
  wide?: boolean;
  /** Icon shown before the label. */
  icon?: string;
}

/** The standard push button. Type defaults to "button" so it never submits a form by accident. */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", large, wide, icon, className, children, type = "button", ...rest },
  ref,
) {
  const cls = ["btn", VARIANT[variant], large && "btn-lg", wide && "wide", className].filter(Boolean).join(" ");
  return (
    <button ref={ref} type={type} className={cls} {...rest}>
      {icon && <Icon name={icon} size={15} />}
      {children}
    </button>
  );
});

/** A button-styled link that opens in a new window. */
export function ButtonLink({ variant = "secondary", className, children, ...rest }: AnchorHTMLAttributes<HTMLAnchorElement> & { variant?: Variant }) {
  return (
    <a className={["btn", VARIANT[variant], className].filter(Boolean).join(" ")} target="_blank" rel="noopener noreferrer" {...rest}>
      {children}
    </a>
  );
}

interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  icon: string;
  /** Accessible name; also the tooltip unless `tooltip` is given. */
  label: string;
  tooltip?: string | false;
  tooltipAlign?: "end";
  tooltipPlacement?: "above";
  size?: number;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon, label, tooltip, tooltipAlign, tooltipPlacement, size = 18, className, type = "button", ...rest },
  ref,
) {
  const text = tooltip === undefined ? undefined : tooltip;
  return (
    <button
      ref={ref}
      type={type}
      className={className ? "ib " + className : "ib"}
      aria-label={label}
      {...(text ? tip(text, { align: tooltipAlign, placement: tooltipPlacement }) : {})}
      {...rest}
    >
      <Icon name={icon} size={size} />
    </button>
  );
});

/** Quiet text button used for in-line actions (“再处理一份”, “去处理”). */
export function LinkButton({ className, children, type = "button", ...rest }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button type={type} className={className ? "link " + className : "link"} {...rest}>
      {children as ReactNode}
    </button>
  );
}
