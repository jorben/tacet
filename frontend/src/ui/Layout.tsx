import type { HTMLAttributes, ReactNode, Ref } from "react";
import { Logo } from "./Icon";

interface PageProps extends HTMLAttributes<HTMLDivElement> {
  /** Ref of the scrolling element. */
  scrollRef?: Ref<HTMLDivElement>;
  innerClassName?: string;
  /** Content placed beside the centred column (marginal notes). */
  aside?: ReactNode;
}

/** A scrolling page with a centred column of content. */
export function Page({ className, innerClassName, scrollRef, aside, children, ...rest }: PageProps) {
  return (
    <div ref={scrollRef} className={className ? "page " + className : "page"} {...rest}>
      {aside}
      <div className={innerClassName ? "page-inner " + innerClassName : "page-inner"}>{children}</div>
    </div>
  );
}

/** Full-height centring for cards that stand alone (progress, results). */
export function Center({ children }: { children: ReactNode }) {
  return <div className="center">{children}</div>;
}

/** Page heading row: title on the left, actions on the right. */
export function PageHead({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return (
    <div className="phead">
      <div className="h1">{title}</div>
      <span className="sp" />
      {children}
    </div>
  );
}

export function Empty({ children, mark, padding }: { children: ReactNode; mark?: boolean; padding?: string }) {
  return (
    <div className="empty" style={padding ? { padding } : undefined}>
      {mark && (
        <div className="g">
          <Logo size={36} />
        </div>
      )}
      {children}
    </div>
  );
}
