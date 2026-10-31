import { useEffect } from "react";
import { closeModal, closePop, overlayStore, useOverlay } from "../../overlay/store";
import { MenuList } from "../../overlay/MenuList";
import { Popover } from "../../overlay/Popover";
import { Toast } from "../../overlay/Toast";
import { ModalContent } from "../dialogs/ModalHost";
import { SampleDrawer } from "../batch/SampleDrawer";
import { UnprocessableFiles } from "../batch/UnprocessableFiles";
import { HitPopover, KeyboardHelp, SelectionPicker } from "../single/popovers";
import type { PopSpec } from "../../overlay/store";

function PopContent({ spec }: { spec: PopSpec }) {
  const c = spec.content;
  switch (c.kind) {
    case "menu": return <MenuList items={c.items} onPick={c.onPick} />;
    case "hit": return <HitPopover hitId={c.hitId} />;
    case "keys": return <KeyboardHelp />;
    case "selection": return <SelectionPicker selection={c.selection} />;
    case "badFiles": return <UnprocessableFiles />;
  }
}

const FOCUSABLE = "button:not([disabled]),a[href],input:not([disabled]),textarea:not([disabled])";

/** Clicking away and Esc close what is open; Tab stays inside a dialog. Registered in the capture phase so it runs before the page's own handlers. */
function useDismissal() {
  useEffect(() => {
    const onClick = (ev: MouseEvent) => {
      const target = ev.target as Element | null;
      const sel = window.getSelection();
      const selecting = !!sel && !sel.isCollapsed && !!target?.closest?.("#doc");
      if (!selecting && !target?.closest?.("#layer .pop")) closePop();
    };
    const onKey = (ev: KeyboardEvent) => {
      const { pop, modal, drawer } = overlayStore.get();
      if (ev.key === "Escape") {
        if (pop) { closePop(); ev.preventDefault(); }
        else if (modal || drawer) { closeModal(); ev.preventDefault(); }
        return;
      }
      if (ev.key === "Tab" && modal) {
        const box = document.querySelector<HTMLElement>("#layer .modal");
        const items = box ? [...box.querySelectorAll<HTMLElement>(FOCUSABLE)] : [];
        if (!items.length) return;
        const first = items[0]!;
        const last = items[items.length - 1]!;
        if (ev.shiftKey && document.activeElement === first) { ev.preventDefault(); last.focus(); }
        else if (!ev.shiftKey && document.activeElement === last) { ev.preventDefault(); first.focus(); }
      }
    };
    document.addEventListener("click", onClick, true);
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("click", onClick, true);
      document.removeEventListener("keydown", onKey, true);
    };
  }, []);
}

/** The layer above the pages: dialogs, drawers, menus and popovers, and the toast. */
export function OverlayHost() {
  const { pop, modal, drawer, toast } = useOverlay((s) => s);
  useDismissal();
  return (
    <div id="layer">
      {modal && <ModalContent key={modal.uid} spec={modal} />}
      {drawer && <SampleDrawer key={drawer.uid} category={drawer.category} rows={drawer.rows} />}
      {pop && (
        <Popover key={pop.uid} spec={pop}>
          <PopContent spec={pop} />
        </Popover>
      )}
      {toast && <Toast key={toast.id} toast={toast} />}
    </div>
  );
}
