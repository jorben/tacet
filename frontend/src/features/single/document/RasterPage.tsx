import { memo, useEffect, useRef } from "react";
import { clickHit } from "../../../actions/single";
import { ensureRasterPage } from "../../../actions/live";
import { CATEGORIES } from "../../../domain/catalog";
import type { Single } from "../../../domain/types";
import { categoryColor } from "../../../ui";

interface Props {
  single: Single;
  pageIndex: number;
  revealed: boolean;
  effectColumn: boolean;
  pageId: boolean;
  hitIds: boolean;
}

/** URL of a rendered page; served by the desktop host from memory, never cached. */
export const pageUrl = (token: string): string => "/tacet-preview/" + token + ".png";

/** One page of a PDF as the engine rendered it, with the candidates outlined where they lie. */
export const RasterPage = memo(function RasterPage({ single, pageIndex, revealed, effectColumn, pageId, hitIds }: Props) {
  const live = single.live!;
  const page = live.raster!.pages[pageIndex];
  const failed = live.raster!.failed[pageIndex];
  const box = useRef<HTMLDivElement>(null);
  // A long document is not rendered all at once: a page is asked for when it comes near the viewport.
  // Without IntersectionObserver (old webviews, tests) every page is asked for at once.
  useEffect(() => {
    if (page || failed) return;
    const el = box.current;
    if (!el || typeof IntersectionObserver === "undefined") {
      void ensureRasterPage(pageIndex);
      return;
    }
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        io.disconnect();
        void ensureRasterPage(pageIndex);
      }
    }, { rootMargin: "800px 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [page, failed, pageIndex, single.runId]);
  const hitOf = new Map<number, number>();
  live.findingOf.forEach((f, i) => hitOf.set(f, i));
  const firstBox = new Set<number>();
  return (
    <div
      ref={box}
      className={effectColumn ? "paper fx raster" : "paper raster"}
      id={pageId ? "pg" + pageIndex : undefined}
      data-pg={pageId ? "" : undefined}
      style={{ padding: 0, minHeight: 0, lineHeight: 0, cursor: "default" }}
    >
      {page ? (
        <div style={{ position: "relative", width: "100%", aspectRatio: page.width + " / " + page.height }}>
          <img src={pageUrl(page.image)} alt={"第 " + (pageIndex + 1) + " 页"} draggable={false} style={{ width: "100%", height: "100%", display: "block" }} />
          {page.boxes.map((b, i) => {
            const id = hitOf.get(b.finding);
            const hit = id == null ? null : single.hits[id];
            if (!hit) return null;
            const [l, t, r, bt] = b.rect;
            const first = !firstBox.has(b.finding);
            firstBox.add(b.finding);
            const covered = revealed && hit.state === "redact";
            const cls = ["hit", "rbox", hit.state === "keep" && "keep", single.focus === id && "focus"].filter(Boolean).join(" ");
            return (
              <button
                key={i}
                type="button"
                className={cls}
                data-hit={id}
                id={hitIds && first ? "h" + id : undefined}
                aria-label={CATEGORIES[hit.type].l + (hit.state === "keep" ? "（保留）" : "（涂抹）")}
                style={{
                  ...categoryColor(hit.type),
                  position: "absolute",
                  left: (l / page.width) * 100 + "%",
                  top: (t / page.height) * 100 + "%",
                  width: ((r - l) / page.width) * 100 + "%",
                  height: ((bt - t) / page.height) * 100 + "%",
                  padding: 0,
                  border: hit.state === "keep" ? "1.5px dotted #6b7280" : "1.5px solid var(--c)",
                  background: covered ? "#111" : hit.state === "keep" ? "none" : "color-mix(in srgb,var(--c) 16%,transparent)",
                  boxShadow: "none",
                  cursor: "pointer",
                }}
                onClick={(e) => {
                  e.stopPropagation();
                  clickHit(id!, effectColumn);
                }}
              />
            );
          })}
        </div>
      ) : (
        <div className="raster-wait" style={{ minHeight: 480, display: "grid", placeItems: "center", color: "#777", fontSize: 13, lineHeight: 1.5 }}>
          {failed ? "此页无法预览" : "正在渲染…"}
        </div>
      )}
      <div className="pgno" style={{ lineHeight: 1.5 }}>
        {pageIndex + 1} / {single.pages.length}
      </div>
    </div>
  );
});
