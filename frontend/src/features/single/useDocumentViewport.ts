import { useCallback, useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import { setViewMode } from "../../actions/single";
import type { ViewMode } from "../../domain/types";

/** Compare view: the two pages share the width, so the pages are scaled to fit. */
export function useFitCompare(scroll: RefObject<HTMLElement>, active: boolean): void {
  useLayoutEffect(() => {
    if (!active) return;
    const fit = () => {
      const sc = scroll.current;
      if (sc) sc.style.setProperty("--z", Math.min(1, Math.max(0.4, (sc.clientWidth - 40 - 16) / 2 / 640)).toFixed(3));
    };
    fit();
    window.addEventListener("resize", fit);
    return () => {
      window.removeEventListener("resize", fit);
      scroll.current?.style.removeProperty("--z");
    };
  }, [scroll, active]);
}

/**
 * Switching between original, effect and compare re-lays-out the pages; this keeps the reader at the same
 * place of the same page.
 */
export function useViewModeSwitch(scroll: RefObject<HTMLElement>, mode: ViewMode, page: number) {
  const saved = useRef<number | null>(null);
  const changeMode = useCallback(
    (next: ViewMode) => {
      if (next === mode) return;
      const sc = scroll.current;
      const pg = document.getElementById("pg" + page);
      if (sc && pg) {
        const a = sc.getBoundingClientRect();
        const b = pg.getBoundingClientRect();
        saved.current = (a.top - b.top) / b.height;
      }
      setViewMode(next);
    },
    [scroll, mode, page],
  );
  useLayoutEffect(() => {
    const frac = saved.current;
    if (frac == null) return;
    saved.current = null;
    const sc = scroll.current;
    const pg = document.getElementById("pg" + page);
    if (!sc || !pg) return;
    sc.style.scrollBehavior = "auto";
    const a = sc.getBoundingClientRect();
    const b = pg.getBoundingClientRect();
    sc.scrollTop += b.top - a.top + Math.max(0, frac) * b.height;
    sc.style.scrollBehavior = "";
  }, [mode]); // eslint-disable-line react-hooks/exhaustive-deps
  return changeMode;
}

/** The scroll position decides which page is “current” (highlighted in the thumbnails). */
export function useCurrentPage(scroll: RefObject<HTMLElement>, onPage: (p: number) => void): void {
  useEffect(() => {
    const sc = scroll.current;
    if (!sc) return;
    const onScroll = () => {
      const top = sc.scrollTop + sc.clientHeight * 0.35;
      let p = 0;
      sc.querySelectorAll<HTMLElement>("[data-pg]").forEach((e, i) => {
        if (e.offsetTop <= top) p = i;
      });
      onPage(p);
    };
    sc.addEventListener("scroll", onScroll, { passive: true });
    return () => sc.removeEventListener("scroll", onScroll);
  }, [scroll, onPage]);
}
