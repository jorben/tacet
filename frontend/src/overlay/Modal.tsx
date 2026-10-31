import { useEffect, useRef, type ReactNode } from "react";
import { closeModal } from "./store";

interface ModalProps {
  children: ReactNode;
  className?: string;
  /** Id of the element that names the dialog. */
  labelledBy?: string;
  /** Which element gets focus when the dialog opens; the first input or primary button by default. */
  initialFocus?: string;
}

/** Dialog frame: a dimmed backdrop and the panel. Focus moves in on open and returns to where it was on close. */
export function Modal({ children, className, labelledBy, initialFocus = "input, .btn-p" }: ModalProps) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>(initialFocus)?.focus();
    return () => {
      if (before?.isConnected) before.focus();
    };
  }, [initialFocus]);
  return (
    <>
      <div className="scrim" onClick={closeModal} />
      <div ref={ref} className={className ? "modal " + className : "modal"} role="dialog" aria-modal="true" aria-labelledby={labelledBy}>
        {children}
      </div>
    </>
  );
}

export function ModalHeader({ title, subtitle, titleId }: { title: ReactNode; subtitle?: ReactNode; titleId?: string }) {
  return (
    <>
      <h3 id={titleId}>{title}</h3>
      {subtitle != null && <div className="ms">{subtitle}</div>}
    </>
  );
}

export function ModalBody({ children }: { children: ReactNode }) {
  return <div className="mb">{children}</div>;
}

/** Buttons at the bottom. A destructive choice that should sit on the far left takes `style={pushLeft}`. */
export function ModalFooter({ children, spaced }: { children: ReactNode; spaced?: boolean }) {
  return (
    <div className="mf" style={spaced ? { marginTop: 22 } : undefined}>
      {children}
    </div>
  );
}
