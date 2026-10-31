import { Fragment, memo } from "react";
import { treatmentOf } from "../../../domain/single";
import type { Block, Single } from "../../../domain/types";
import { HitSpan } from "./HitSpan";
import { ImageBlock } from "./ImageBlock";

interface PaperProps {
  single: Single;
  pageIndex: number;
  /** Hits are shown as their result (effect view, or the effect copy in the compare view). */
  revealed: boolean;
  /** This is the effect copy in the compare view. */
  effectColumn: boolean;
  /** The page itself carries the id the thumbnails scroll to (in the compare view the row does). */
  pageId: boolean;
  /** The hits carry DOM ids (only one copy of a hit may). */
  hitIds: boolean;
}

/** One page of the document. */
export const Paper = memo(function Paper({ single, pageIndex, revealed, effectColumn, pageId, hitIds }: PaperProps) {
  const page = single.pages[pageIndex]!;
  const readOnly = single.from === "batch";

  const segments = (b: Block, bi: number) =>
    (b.segs || []).map((g, si) =>
      g.h != null ? (
        <HitSpan
          key={si}
          hit={single.hits[g.h]!}
          treatment={treatmentOf(single, single.hits[g.h]!.type)}
          focused={single.focus === g.h}
          revealed={revealed}
          effectColumn={effectColumn}
          withId={hitIds}
        />
      ) : (
        <span key={si} className="tx" data-k={pageIndex + "." + bi + "." + si}>
          {g.t}
        </span>
      ),
    );
  const image = (index: number) => <ImageBlock image={single.imgs[index]!} index={index} ext={single.ext} revealed={revealed} readOnly={readOnly} />;

  const block = (b: Block, bi: number) => {
    switch (b.k) {
      case "title": return <div className="d-title">{segments(b, bi)}</div>;
      case "meta": return <div className="d-meta">{segments(b, bi)}</div>;
      case "h": return <h4>{segments(b, bi)}</h4>;
      case "pi": return <p className="ind">{segments(b, bi)}</p>;
      case "img": return image(b.img!);
      case "sign":
        return (
          <div className="d-sign">
            <div>
              <p>甲方（盖章）：</p>
              {image(b.img!)}
            </div>
            <div>
              <p>{segments(b, bi)}</p>
              <p>日期：2026 年 4 月 17 日</p>
            </div>
          </div>
        );
      default: return <p>{segments(b, bi)}</p>;
    }
  };

  return (
    <div className={effectColumn ? "paper fx" : "paper"} id={pageId ? "pg" + pageIndex : undefined} data-pg={pageId ? "" : undefined}>
      {page.map((b, bi) => (
        <Fragment key={bi}>{block(b, bi)}</Fragment>
      ))}
      <div className="pgno">
        {single.ext === "docx" ? "约 " : ""}
        {pageIndex + 1} / {single.pages.length}
      </div>
    </div>
  );
});
