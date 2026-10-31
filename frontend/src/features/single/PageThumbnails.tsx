import { categoryColor } from "../../ui";
import type { Single } from "../../domain/types";

/** Small page previews at the left: coloured ticks show where hits are; clicking scrolls there. */
export function PageThumbnails({ single, onGoto }: { single: Single; onGoto: (page: number) => void }) {
  return (
    <div className="thumbs" id="thumbs">
      {single.pages.map((pg, pi) => {
        const marks: JSX.Element[] = [];
        const n = pg.length;
        pg.forEach((b, bi) => {
          if (b.k === "img") marks.push(<i key={`i${bi}`} className="im" style={{ right: "14%", top: "12%" }} />);
          if (b.k === "sign") marks.push(<i key={`s${bi}`} className="im" style={{ left: "16%", top: "80%", borderRadius: "50%", width: "22%", height: "14%" }} />);
          (b.segs || []).forEach((g, si) => {
            if (g.h == null) return;
            const h = single.hits[g.h]!;
            if (h.state === "redact") {
              marks.push(<i key={`h${bi}.${si}`} style={{ ...categoryColor(h.type), top: (10 + (bi / n) * 78).toFixed(1) + "%", left: 18 + (si % 3) * 18 + "%" }} />);
            }
          });
        });
        return (
          <button key={pi} type="button" className={single.page === pi ? "thumb on" : "thumb"} aria-label={(single.ext === "docx" ? "约" : "") + "第 " + (pi + 1) + " 页"} onClick={() => onGoto(pi)}>
            {marks}
            <b>{pi + 1}</b>
          </button>
        );
      })}
    </div>
  );
}
