import { useRef } from "react";
import { outputName } from "../../domain/format";
import { redactedCount } from "../../domain/single";
import type { Single } from "../../domain/types";
import { useAppState } from "../../store";
import { Steps, FileIcon } from "../../ui";
import { DocumentPane } from "./DocumentPane";
import { PageThumbnails } from "./PageThumbnails";
import { ReviewSidebar } from "./ReviewSidebar";
import { useViewModeSwitch } from "./useDocumentViewport";
import { useSingleShortcuts } from "./useSingleShortcuts";

function ExportOverlay({ single }: { single: Single }) {
  return (
    <div className="overlay">
      <div className="card proc">
        <div className="proc-f">
          <FileIcon ext={single.ext} />
          <div>
            <div className="fn">{outputName(single.name, single.ext)}</div>
            <div className="fm">涂抹 {redactedCount(single)} 处</div>
          </div>
        </div>
        <div id="procSteps">
          <Steps labels={["导出", "验证"]} stage={single.stage} progress={single.sp} />
        </div>
      </div>
    </div>
  );
}

/** Reviewing one document: pages, the document itself, and the decisions on the right. */
export function SingleReview({ single }: { single: Single }) {
  const undoCount = useAppState((s) => s.undo.length);
  const scrollRef = useRef<HTMLDivElement>(null);
  const changeMode = useViewModeSwitch(scrollRef, single.mode, single.page);
  useSingleShortcuts(changeMode);
  const gotoPage = (page: number) => {
    const el = document.getElementById("pg" + page);
    if (el) scrollRef.current?.scrollTo({ top: el.offsetTop - 24, behavior: "smooth" });
  };
  return (
    <>
      <div className={single.mode === "cmp" ? "review cmp" : "review"}>
        <PageThumbnails single={single} onGoto={gotoPage} />
        <DocumentPane single={single} undoCount={undoCount} scrollRef={scrollRef} onMode={changeMode} />
        <ReviewSidebar single={single} undoCount={undoCount} />
      </div>
      {single.phase === "exporting" && <ExportOverlay single={single} />}
    </>
  );
}
