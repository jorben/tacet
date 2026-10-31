import { useLayoutEffect, useRef } from "react";
import { openReviewedFile } from "../../actions/review";
import { NO_DOCUMENT_GROUPS } from "../../domain/catalog";
import { decisionLabel, previewDecision, reviewWhere } from "../../domain/review";
import type { Batch, ReviewItem, ViewMode } from "../../domain/types";
import { Button, FileIcon, Icon, IconButton, Pill, Segmented } from "../../ui";
import { useFitCompare } from "../single/useDocumentViewport";
import { ReviewPaper } from "./ReviewPaper";

/** Pages for the groups that have no document to show are cards that describe the file instead. */
function FileCard({ batch: B, item: it }: { batch: Batch; item: ReviewItem }) {
  const g = B.groups[it.gi]!;
  const f = B.files[it.fi]!;
  if (g.id === "pwd") {
    return (
      <div className="rvbox">
        <div className="card" style={{ width: 340, padding: "36px 28px", textAlign: "center" }}>
          <div className="done-ic" style={{ background: "var(--surface-2)", color: "var(--ink-2)" }}>
            <Icon name="lock" size={26} />
          </div>
          <div style={{ fontWeight: 600 }}>{f.name}</div>
          <div className="muted sm" style={{ marginTop: 4 }}>
            已加密
          </div>
        </div>
      </div>
    );
  }
  if (g.id === "fail") {
    return (
      <div className="rvbox">
        <div className="card" style={{ width: 440, padding: "30px 28px" }}>
          <div style={{ display: "flex", gap: 12, alignItems: "center" }}>
            <FileIcon ext={f.ext} />
            <div>
              <div style={{ fontWeight: 600 }}>{f.name}</div>
              <div className="muted sm">整份文件处理失败</div>
            </div>
          </div>
          <dl className="kv" style={{ marginTop: 20 }}>
            <dt>原因</dt>
            <dd>{f.failWhy || ""}</dd>
            <dt>结果</dt>
            <dd>
              <Pill tone="bad">未导出</Pill>
            </dd>
          </dl>
          <div className="muted sm" style={{ marginTop: 16 }}>
            重新处理会从失败的步骤再执行一次；仍失败将标记为无法处理
          </div>
        </div>
      </div>
    );
  }
  return (
    <div className="rvbox">
      <div className="card" style={{ width: 440, padding: "30px 28px" }}>
        <div style={{ display: "flex", gap: 12, alignItems: "center" }}>
          <FileIcon ext={f.ext} />
          <div>
            <div style={{ fontWeight: 600 }}>{f.name}</div>
            <div className="muted sm num">
              {f.pages.toLocaleString("zh-CN")} 页 · {reviewWhere(g, f, it)}
            </div>
          </div>
        </div>
        <div className="muted sm" style={{ marginTop: 16 }}>
          逐处查看后再确认导出
        </div>
        <Button icon="ext" style={{ marginTop: 14 }} onClick={openReviewedFile}>
          打开逐处复核
        </Button>
      </div>
    </div>
  );
}

/** The document around the current item, in the chosen view, with the toolbar. */
export function ReviewDocument({ batch: B, item: it, mode, onMode }: { batch: Batch; item: ReviewItem; mode: ViewMode; onMode: (m: ViewMode) => void }) {
  const g = B.groups[it.gi]!;
  const f = B.files[it.fi]!;
  const rv = B.rv!;
  const hasDocument = !NO_DOCUMENT_GROUPS.includes(g.id);
  const scroll = useRef<HTMLDivElement>(null);
  useFitCompare(scroll, hasDocument && mode === "cmp");
  // Bring the element the decision is about into view.
  useLayoutEffect(() => {
    const sc = scroll.current;
    const target = sc?.querySelector<HTMLElement>(".tgt");
    if (sc && target) sc.scrollTop = Math.max(0, target.getBoundingClientRect().top - sc.getBoundingClientRect().top - sc.clientHeight * 0.32);
  }, [it.key, mode]);

  const decision = previewDecision(rv, it, g);
  const effect = (
    <div id="rvFx" className={rv.hov ? "still" : undefined}>
      <ReviewPaper group={g} file={f} item={it} view="fx" decision={decision} />
    </div>
  );
  return (
    <div className="rvc">
      <div className="doc-tools">
        <span className="rvt">{f.name}</span>
        <span className="muted sm" style={{ whiteSpace: "nowrap" }}>
          {reviewWhere(g, f, it)}
        </span>
        {hasDocument && mode === "fx" && (
          <Pill tone="pri" id="rvFxL" style={{ marginLeft: 6 }}>
            预览 · {decisionLabel(g, decision)}
          </Pill>
        )}
        <span className="sp" />
        {hasDocument && (
          <Segmented
            label="视图"
            value={mode}
            onChange={onMode}
            options={[
              { value: "orig", label: "原文" },
              { value: "fx", label: "效果" },
              { value: "cmp", label: "对比", icon: "columns", tooltip: "左右对比 C", tooltipAlign: "end" },
            ]}
          />
        )}
        {hasDocument && <IconButton icon="ext" size={16} label="打开整份文件" tooltip="打开整份文件" tooltipAlign="end" style={{ marginLeft: 4 }} onClick={openReviewedFile} />}
      </div>
      {hasDocument ? (
        <div ref={scroll} className={mode === "cmp" ? "doc-scroll cmpv" : "doc-scroll"} id="rvDoc">
          {mode === "cmp" ? (
            <>
              <div className="cmp-h">
                <span>原文</span>
                <span id="rvFxL">效果预览 · {decisionLabel(g, decision)}</span>
              </div>
              <div className="cmp-row">
                <ReviewPaper group={g} file={f} item={it} view="orig" />
                {effect}
              </div>
            </>
          ) : mode === "fx" ? (
            effect
          ) : (
            <ReviewPaper group={g} file={f} item={it} view="orig" />
          )}
        </div>
      ) : (
        <FileCard batch={B} item={it} />
      )}
    </div>
  );
}
