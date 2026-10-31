import { useLayoutEffect, useRef } from "react";
import { selectReviewItem } from "../../actions/review";
import { decisionLabel, reviewWhere } from "../../domain/review";
import type { Batch } from "../../domain/types";
import { Empty, Icon, Tag } from "../../ui";

/** All items waiting for a decision, grouped by exception type and file. The current one stays in view. */
export function ReviewQueue({ batch: B }: { batch: Batch }) {
  const rv = B.rv!;
  const ref = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    const q = ref.current;
    const current = q?.querySelector<HTMLElement>("[aria-current=true]");
    if (!q || !current) return;
    const a = current.getBoundingClientRect();
    const b = q.getBoundingClientRect();
    if (a.top < b.top + 40 || a.bottom > b.bottom - 40) q.scrollTop += a.top - b.top - b.height * 0.4;
  }, [rv.cur]);

  let lastGroup: string | null = null;
  let lastFile: number | null = null;
  const rows = rv.items.map((it) => {
    const g = B.groups[it.gi]!;
    const f = B.files[it.fi]!;
    const sub = it.gid === lastGroup && it.fi === lastFile;
    lastFile = it.fi;
    const heading =
      it.gid !== lastGroup ? (
        <div className="rvg" key={"g" + it.gid}>
          <Icon name={g.ic} size={14} />
          <span>{g.t}</span>
          <span className="n num">{rv.items.filter((x) => x.gid === it.gid && !x.dec).length || <Icon name="check" size={13} strokeWidth={2.4} />}</span>
        </div>
      ) : null;
    lastGroup = it.gid;
    return [
      heading,
      <button key={it.key} type="button" className={["rvi", it.dec && "dn", sub && "sub"].filter(Boolean).join(" ")} aria-current={it.key === rv.cur} onClick={() => selectReviewItem(it.key)}>
        <span className={it.dec ? "st ok" : "st"}>{it.dec && <Icon name="check" size={10} strokeWidth={3} />}</span>
        <span className="x">
          {!sub && <div className="fn">{f.name}</div>}
          <div className="fm">
            {reviewWhere(g, f, it)}
            {g.found?.[it.fi] && (
              <>
                {" "}
                <Tag>导出后发现</Tag>
              </>
            )}
          </div>
        </span>
        {it.dec && <span className="dl">{decisionLabel(g, it.dec)}</span>}
      </button>,
    ];
  });
  return (
    <nav className="rvq" id="rvq" aria-label="复核队列" ref={ref}>
      {rv.items.length ? rows : <Empty padding="48px 0">—</Empty>}
    </nav>
  );
}
