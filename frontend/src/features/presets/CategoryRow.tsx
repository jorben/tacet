import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useScrollRequests } from "../../actions/scrollRequests";
import type { CategoryId, PresetTab } from "../../domain/types";
import { useAppState } from "../../store";
import { Swatch, Tag } from "../../ui";

/**
 * A category's name. While the catalog is being read, a category it does not know yet shows a placeholder bar
 * rather than its raw id; once read (or failed), an id the catalog does not know is shown as is and marked.
 */
export function CategoryName({ category }: { category: CategoryId }) {
  const known = useAppState((s) => s.categories.find((c) => c.id === category));
  const loading = useAppState((s) => s.categoryStatus === "loading");
  if (known) return <span className="rl cat-name" title={known.name}>{known.name}</span>;
  if (loading) return <span className="rl cat-name" aria-label="正在读取类别名称"><span className="skel" /></span>;
  return (
    <>
      <span className="rl cat-name" title={category}>{category}</span>
      <Tag>未登记类别</Tag>
    </>
  );
}

interface CategoryRowProps {
  category: CategoryId;
  /** Tags after the name: where the category comes from, what changed. */
  tags?: ReactNode;
  /** A second line under the name (the labels it is mapped from, …). */
  sub?: ReactNode;
  /** Controls on the right. */
  children?: ReactNode;
  className?: string;
}

/**
 * One category, laid out the same on the three tabs: swatch, name (one width rule, truncated with a title), tags,
 * an optional second line, and the controls on the right. Swatch and name start at the same x everywhere.
 */
export function CategoryRow({ category, tags, sub, children, className }: CategoryRowProps) {
  return (
    <div className={className ? "row cat-row " + className : "row cat-row"} data-category={category}>
      <Swatch category={category} />
      <div className="cat-body">
        <div className="cat-line">
          <CategoryName category={category} />
          {tags}
        </div>
        {sub != null && <div className="cat-sub">{sub}</div>}
      </div>
      {children != null && <span className="rv">{children}</span>}
    </div>
  );
}

/**
 * Scrolls the tab's pane to the category a link from another tab points at, and marks it briefly. Returns the ref
 * of the pane to search in.
 */
export function useCategoryFocus(tab: PresetTab) {
  const pane = useRef<HTMLDivElement>(null);
  const [target, setTarget] = useState<{ category: CategoryId } | null>(null);
  useScrollRequests((r) => { if (r.kind === "category") setTarget({ category: r.category }); }, (r) => r.kind === "category" && r.tab === tab);
  useLayoutEffect(() => {
    if (!target) return;
    const row = [...(pane.current?.querySelectorAll<HTMLElement>("[data-category]") ?? [])].find((e) => e.dataset.category === target.category);
    if (row) {
      row.scrollIntoView?.({ block: "center" });
      row.classList.add("cat-flash");
      row.addEventListener("animationend", () => row.classList.remove("cat-flash"), { once: true });
    }
    setTarget(null);
  }, [target]);
  return pane;
}
