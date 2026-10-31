import { useEffect, useRef, type RefObject } from "react";
import { clearReveal, openRevealedHit, setCurrentPage, showKeyboardHelp, undoSingle } from "../../actions/single";
import type { Single, ViewMode } from "../../domain/types";
import { Icon, IconButton, Segmented } from "../../ui";
import { tip } from "../../ui/tip";
import { DocumentView } from "./document/DocumentView";
import { setSelectionPopover } from "./selection";
import { useCurrentPage, useFitCompare } from "./useDocumentViewport";

interface DocumentPaneProps {
  single: Single;
  undoCount: number;
  scrollRef: RefObject<HTMLDivElement>;
  onMode: (m: ViewMode) => void;
}

/** Toolbar and the scrolling document. */
export function DocumentPane({ single, undoCount, scrollRef, onMode }: DocumentPaneProps) {
  const cmp = single.mode === "cmp";
  useFitCompare(scrollRef, cmp);
  useCurrentPage(scrollRef, setCurrentPage);

  // A hit chosen from the list or the keyboard is scrolled into view, then its popover opens.
  const reveal = single.reveal;
  const revealTimer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(revealTimer.current), []);
  useEffect(() => {
    if (!reveal) return;
    clearTimeout(revealTimer.current);
    clearReveal();
    document.getElementById("h" + reveal.id)?.scrollIntoView({ block: "center", behavior: "smooth" });
    revealTimer.current = setTimeout(() => {
      if (document.getElementById("h" + reveal.id)) openRevealedHit(reveal.id);
    }, 260);
  }, [reveal]);

  return (
    <div className="docwrap">
      <div className="doc-tools">
        <Segmented
          label="视图"
          value={single.mode}
          onChange={onMode}
          options={[
            { value: "orig", label: "原文" },
            { value: "fx", label: "效果" },
            { value: "cmp", label: "对比", icon: "columns", tooltip: "左右对比 C" },
          ]}
        />
        <span className="sp" />
        {single.mode === "orig" ? (
          <span className="tool-hint">
            <Icon name="mark" size={14} />
            选中文字可补充涂抹
          </span>
        ) : (
          <span className="tool-hint" tabIndex={0} {...tip("按当前决定模拟，导出后以验证结果为准", { align: "end" })}>
            <Icon name="info" size={14} />
            效果为预览
          </span>
        )}
        <IconButton icon="undo" size={17} label="撤销" tooltip="撤销 ⌘Z" tooltipAlign="end" disabled={!undoCount} style={undoCount ? undefined : { opacity: 0.35 }} onClick={undoSingle} />
        <IconButton icon="kbd" label="快捷键" tooltip="快捷键" tooltipAlign="end" onClick={(e) => showKeyboardHelp(e.currentTarget)} />
      </div>
      <DocumentScroll single={single} scrollRef={scrollRef} cmp={cmp} />
    </div>
  );
}

function DocumentScroll({ single, scrollRef, cmp }: { single: Single; scrollRef: RefObject<HTMLDivElement>; cmp: boolean }) {
  return (
    <div ref={scrollRef} className={cmp ? "doc-scroll cmpv" : "doc-scroll"} id="docScroll" onMouseUp={setSelectionPopover}>
      <DocumentView single={single} />
    </div>
  );
}
