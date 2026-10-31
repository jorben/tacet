import type { CSSProperties, HTMLAttributes, ReactNode } from "react";
import { Icon } from "./Icon";

export function List({ className, children, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={className ? "list " + className : "list"} {...rest}>
      {children}
    </div>
  );
}

interface RowProps {
  /** Leading icon. */
  icon?: string;
  iconSize?: number;
  /** Leading element used instead of an icon (e.g. a colour swatch). */
  lead?: ReactNode;
  title: ReactNode;
  /** Secondary line under the title. */
  description?: ReactNode;
  /** Controls on the right. */
  children?: ReactNode;
  className?: string;
  /** Extra class on the controls container. */
  controlsClassName?: string;
  /** Extra attributes on the description (live regions, ids). */
  descriptionProps?: HTMLAttributes<HTMLDivElement>;
  descriptionClassName?: string;
  style?: CSSProperties;
  /** Make the whole row a button. */
  onClick?: () => void;
}

/** One line of a list: icon, title with optional description, and the control that changes it. */
export function Row({ icon, iconSize = 17, lead, title, description, children, className, controlsClassName, descriptionProps, descriptionClassName, style, onClick }: RowProps) {
  const body = (
    <>
      {lead}
      {icon && (
        <span className="ri">
          <Icon name={icon} size={iconSize} />
        </span>
      )}
      {description != null ? (
        <div>
          <div className="rl">{title}</div>
          <div className={descriptionClassName ? "rs " + descriptionClassName : "rs"} {...descriptionProps}>
            {description}
          </div>
        </div>
      ) : (
        <span className="rl">{title}</span>
      )}
      {children != null && <span className={controlsClassName ? "rv " + controlsClassName : "rv"}>{children}</span>}
    </>
  );
  const cls = className ? "row " + className : "row";
  return onClick ? (
    <button type="button" className={cls} style={style} onClick={onClick}>
      {body}
    </button>
  ) : (
    <div className={cls} style={style}>
      {body}
    </div>
  );
}

/** Heading above a group of lists. */
export function SectionTitle({ children, first, className, ...rest }: HTMLAttributes<HTMLDivElement> & { first?: boolean }) {
  return (
    <div className={className ? "sec-t " + className : "sec-t"} style={first ? { marginTop: 8 } : undefined} {...rest}>
      {children}
    </div>
  );
}
