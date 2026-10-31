import type { ReactNode } from "react";
import { resumeBatchReview } from "../../actions/review";
import { setBatchPhase } from "../../actions/batch";
import { decisionLabel, reviewExportSummary } from "../../domain/review";
import { fmt } from "../../domain/format";
import type { Batch } from "../../domain/types";
import { Button, Icon, ResultCard, SegmentBar } from "../../ui";

/** How far the background export of the reviewed files has got, and what the user can do next. */
function ExportProgress({ batch: B }: { batch: Batch }) {
  const x = reviewExportSummary(B);
  const verb = x.autoPublish ? "导出" : "验证";
  const notes: ReactNode[] = [];
  if (x.otherBusy) notes.push("另有 " + fmt(x.otherBusy) + " 份其他文件处理中");
  if (!x.pending) {
    if (x.settled) notes.push("即将显示结果");
    else if (!x.autoPublish && !x.busy) notes.push(<span style={{ color: "var(--warn)" }}>{fmt(x.readyAll)} 份已通过验证，待你确认导出</span>);
    else if (x.autoPublish) notes.push("完成后自动显示结果");
  }
  const publishNow = !x.pending && !x.autoPublish && x.readyAll > 0 && !x.busy;
  return (
    <>
      {x.total > 0 && (
        <div className="rvx">
          <div className="rvx-h">
            <span>
              {x.left
                ? "正在" + verb + "你复核的 " + fmt(x.total) + " 份"
                : x.ok === x.total
                  ? "你复核的 " + fmt(x.total) + " 份" + (x.autoPublish ? "已导出" : "已通过验证")
                  : "你复核的 " + fmt(x.total) + " 份中 " + fmt(x.total - x.ok) + " 份" + (x.autoPublish ? "未导出" : "未通过验证")}
            </span>
            <span className="num">
              {x.autoPublish ? "已导出 " : "已验证 "}
              {fmt(x.ok)} / {fmt(x.total)}
            </span>
          </div>
          <SegmentBar
            total={x.total}
            segments={[{ count: x.ok, color: "var(--ok)" }, ...(x.autoPublish ? [{ count: x.readyMine, color: "#6C8FC0" }] : []), { count: x.failed, color: "var(--bad)" }]}
          />
          <div className="rvx-s">
            {x.autoPublish ? "每份导出后重新打开验证，通过后写入 " + B.out : "验证通过后由你确认导出"}
            {x.failed ? " · 无法处理 " + fmt(x.failed) + " 份" : ""}
          </div>
        </div>
      )}
      {x.pending > 0 && (
        <div className="rvx-warn">
          <Icon name="alert" size={14} />
          验证中发现 {fmt(x.pending)} 处需要你处理
        </div>
      )}
      {notes.length > 0 && (
        <div className="muted sm rvx-n">
          {notes.map((n, i) => (
            <span key={i}>
              {i > 0 && <br />}
              {n}
            </span>
          ))}
        </div>
      )}
      <div className="acts">
        {x.pending > 0 ? (
          <>
            <Button onClick={() => setBatchPhase("run")}>返回批次</Button>
            <Button variant="primary" onClick={resumeBatchReview}>
              继续复核
            </Button>
          </>
        ) : publishNow ? (
          <Button variant="primary" onClick={() => setBatchPhase("run")}>
            返回批次导出
          </Button>
        ) : (
          <Button onClick={() => setBatchPhase("run")}>返回批次</Button>
        )}
      </div>
    </>
  );
}

/** Shown when every item has a decision. */
export function ReviewDone({ batch: B }: { batch: Batch }) {
  const byLabel: Record<string, number> = {};
  B.rv!.items.forEach((x) => {
    if (!x.dec) return;
    const l = decisionLabel(B.groups[x.gi]!, x.dec);
    byLabel[l] = (byLabel[l] || 0) + 1;
  });
  return (
    <div className="rvbox">
      <ResultCard title="复核完成" stats={Object.entries(byLabel).map(([label, value]) => ({ label, value }))}>
        <div id="rvExp">
          <ExportProgress batch={B} />
        </div>
      </ResultCard>
    </div>
  );
}
