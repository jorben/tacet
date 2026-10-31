import type { ReactNode } from "react";
import { closeModal } from "./store";

/** A panel that slides in from the right, with a backdrop. */
export function Drawer({ children }: { children: ReactNode }) {
  return (
    <>
      <div className="scrim" onClick={closeModal} />
      <div className="drawer" role="dialog" aria-modal="true">
        {children}
      </div>
    </>
  );
}
