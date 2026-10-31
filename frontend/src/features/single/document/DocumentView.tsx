import { useState, type MouseEvent } from "react";
import type { Single } from "../../../domain/types";
import { Paper } from "./Paper";
import { TwinContext } from "./twin";

/** The pages of the document in the chosen view: original, effect, or side by side. */
export function DocumentView({ single }: { single: Single }) {
  const [twin, setTwin] = useState<number | null>(null);
  const pages = single.pages.map((_, i) => i);
  if (single.mode === "cmp") {
    const hover = (e: MouseEvent) => {
      const hit = (e.target as Element).closest<HTMLElement>(".hit");
      const id = hit ? Number(hit.dataset.hit) : null;
      if (id !== twin) setTwin(id);
    };
    return (
      <TwinContext.Provider value={twin}>
        <div className="doc" id="doc" onMouseOver={hover}>
          <div className="cmp-h">
            <span>原文</span>
            <span>效果 · 预览</span>
          </div>
          {pages.map((pi) => (
            <div className="cmp-row" id={"pg" + pi} data-pg="" key={pi}>
              <Paper single={single} pageIndex={pi} revealed={false} effectColumn={false} pageId={false} hitIds />
              <Paper single={single} pageIndex={pi} revealed effectColumn pageId={false} hitIds={false} />
            </div>
          ))}
        </div>
      </TwinContext.Provider>
    );
  }
  const fx = single.mode === "fx";
  return (
    <div className={fx ? "doc fx" : "doc"} id="doc">
      {pages.map((pi) => (
        <Paper key={pi} single={single} pageIndex={pi} revealed={fx} effectColumn={false} pageId hitIds />
      ))}
    </div>
  );
}
