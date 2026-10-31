import { countFiles } from "../../domain/batch";
import { fmt } from "../../domain/format";
import type { Batch } from "../../domain/types";
import { SegmentBar, type BarSegment } from "../../ui";

const PARTIAL_COLOR = "color-mix(in srgb,var(--ok) 55%,transparent)";

export function ExportedCount({ exported, total, caption }: { exported: number; total: number; caption: string }) {
  return (
    <div>
      <div className="big num">
        {fmt(exported)}
        <span className="of"> / {fmt(total)}</span>
      </div>
      <div className="lbl">{caption}</div>
    </div>
  );
}

/** The progress bar of a running batch, with the legend of what each colour means. */
export function BatchProgress({ batch }: { batch: Batch }) {
  const c = countFiles(batch.files);
  const bar: BarSegment[] = [
    { count: c.done, color: "var(--ok)", title: "已导出" },
    { count: c.partial, color: PARTIAL_COLOR, title: "部分结果" },
    { count: c.ready, color: "#6C8FC0", title: "待导出" },
    { count: c.proc, color: "var(--primary)", title: "处理中" },
    { count: c.wait + c.held, color: "var(--warn)", title: "待你处理" },
    { count: c.bad, color: "var(--bad)", title: "无法处理" },
    { count: c.skip, color: "var(--faint)", title: "已跳过" },
  ];
  const legend: [number, string, string][] = [
    [c.done, "var(--ok)", "已导出"],
    [c.partial, PARTIAL_COLOR, "部分结果"],
    [c.ready, "#6C8FC0", "待导出"],
    [c.proc, "var(--primary)", "处理中"],
    [c.queue, "var(--line)", "排队"],
    [c.wait + c.held, "var(--warn)", "待你处理"],
    [c.bad, "var(--bad)", "无法处理"],
    [c.skip, "var(--faint)", "已跳过"],
  ];
  return (
    <div id="bstats">
      <SegmentBar segments={bar} total={batch.n} label="进度" />
      <div className="legend">
        {legend.map(([n, color, label]) =>
          n ? (
            <span key={label}>
              <span className="dot" style={{ color }} />
              {label} <b className="num">{fmt(n)}</b>
            </span>
          ) : null,
        )}
      </div>
    </div>
  );
}
