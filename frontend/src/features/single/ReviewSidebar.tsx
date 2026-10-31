import type { ReactNode, MouseEvent } from "react";
import { returnToBatch, exportSingle, focusHit, focusNextOfType, openAllImagesMenu, openCategoryMenu, openOutputMenu, openSingleSchemeMenu, reviewBatchFileExceptions } from "../../actions/single";
import { REVIEW_CATEGORY_ORDER, CATEGORIES } from "../../domain/catalog";
import { locStr, outputKind } from "../../domain/format";
import { findPresetByName, formatConfidence, presetConfidence } from "../../domain/presets";
import { categoryStats, imageSummary, isPartialResult, orderedHitIds, treatmentOf, unseenLowHits } from "../../domain/single";
import type { Batch, Single } from "../../domain/types";
import { useAppState } from "../../store";
import { Button, FileIcon, Icon, Pill, categoryColor, tip } from "../../ui";

interface SummaryRowProps {
  icon: string;
  children: ReactNode;
  trailing?: ReactNode;
  onClick?: (e: MouseEvent<HTMLButtonElement>) => void;
  /** Tooltip of a read-only row. */
  hint?: string;
}

/** One line of the summary: a button when it can be changed, plain when it cannot. */
function SummaryRow({ icon, children, trailing, onClick, hint }: SummaryRowProps) {
  const body = (
    <>
      <Icon name={icon} size={16} />
      {children}
      <span className="sp" />
      {trailing}
    </>
  );
  return onClick ? (
    <button type="button" className="sum-row" onClick={onClick}>
      {body}
    </button>
  ) : (
    <div className="sum-row ro" tabIndex={0} {...(hint ? tip(hint) : {})}>
      {body}
    </div>
  );
}

function ConfidenceHint({ preset }: { preset: string }) {
  const presets = useAppState((s) => s.presets);
  const models = useAppState((s) => s.models);
  const p = findPresetByName(presets, preset);
  return p.det === "model" ? <span className="muted">置信度 ≥ {formatConfidence(presetConfidence(p, models))}</span> : null;
}

const DOT = { color: "#D18A0F", width: 6, height: 6 } as const;

function Summary({ single, batch }: { single: Single; batch: Batch | null }) {
  const ok = outputKind(single.ext);
  const inBatch = single.from === "batch";
  const partial = isPartialResult(single);
  const file = inBatch && batch ? batch.files[single.bfi!] : null;
  const pending = file ? file.g.reduce((a, gid) => a + (batch!.groups.find((g) => g.id === gid)!.per[file.i] || 0), 0) : 0;
  return (
    <div className="sum">
      {inBatch ? (
        <SummaryRow icon="layers" hint="批次方案 · 开始后不可更改" trailing={<Icon name="lock" size={14} />}>
          <span className="v">{single.preset}</span>
          <ConfidenceHint preset={single.preset} />
        </SummaryRow>
      ) : (
        <SummaryRow icon="layers" onClick={(e) => openSingleSchemeMenu(e.currentTarget)} trailing={<Icon name="chev-d" size={14} />}>
          <span className="v">{single.preset}</span>
          <ConfidenceHint preset={single.preset} />
        </SummaryRow>
      )}
      <SummaryRow icon="fileout" hint={ok.tip} trailing={<Icon name="info" size={14} />}>
        <span className="v">{ok.l}</span>
      </SummaryRow>
      {inBatch ? (
        <SummaryRow icon="image" hint="按批次设置" trailing={<Icon name="lock" size={14} />}>
          <span className="v">图像 {single.imgs.length}</span>
          <span className="muted">{imageSummary(single, ok.verb)}</span>
        </SummaryRow>
      ) : (
        <SummaryRow
          icon="image"
          onClick={(e) => openAllImagesMenu(e.currentTarget)}
          trailing={
            <>
              {partial && <Pill tone="warn">部分结果</Pill>}
              <Icon name="chev-d" size={14} />
            </>
          }
        >
          <span className="v">图像 {single.imgs.length}</span>
          <span className="muted">{imageSummary(single, ok.verb)}</span>
        </SummaryRow>
      )}
      {pending > 0 && (
        <SummaryRow icon="alert" onClick={reviewBatchFileExceptions} trailing={<Icon name="chev-r" size={14} />}>
          <span className="v">还有 {pending} 处待你处理</span>
        </SummaryRow>
      )}
    </div>
  );
}

/** Low-confidence hits that still deserve a look, then the per-category list. */
function Categories({ single }: { single: Single }) {
  const stats = categoryStats(single);
  const order = REVIEW_CATEGORY_ORDER.filter((t) => stats[t]);
  const lowIds = orderedHitIds(single).filter((i) => single.hits[i]!.low);
  const left = unseenLowHits(single).length;
  return (
    <div className="cats" id="cats">
      {left > 0 && (
        <>
          <div className="cats-h">
            <span className="dot" style={DOT} />
            <span>待看</span>
            <span className="num" style={{ color: "var(--ink-2)" }}>
              {left}
            </span>
            <span className="sp" />
            <span className="muted" style={{ fontWeight: 400 }} tabIndex={0} {...tip("置信度较低，已默认涂抹", { align: "end" })}>
              <Icon name="info" size={13} />
            </span>
          </div>
          {lowIds.map((i) => {
            const h = single.hits[i]!;
            return (
              <button key={i} type="button" className={h.seen ? "lowrow seen" : "lowrow"} style={categoryColor(h.type)} onClick={() => focusHit(i)}>
                <span className="sw" />
                <span className="v">{h.text}</span>
                <span className="muted sm">{h.seen ? (h.state === "keep" ? "保留" : "涂抹") : locStr(single.ext, h.page + 1, h.sec, h.para, true)}</span>
                {h.seen && <Icon name="check" size={13} strokeWidth={2.4} />}
              </button>
            );
          })}
        </>
      )}
      <div className="cats-h">
        <span>命中</span>
        <span className="num" style={{ color: "var(--ink-2)" }}>
          {single.hits.length}
        </span>
        <span className="sp" />
        {left === 0 && (
          <Pill tone="ok">
            <Icon name="check" size={12} strokeWidth={2.4} />
            已看完
          </Pill>
        )}
      </div>
      {order.map((t) => {
        const o = stats[t]!;
        return (
          <div className="cat" style={categoryColor(t)} key={t}>
            <button type="button" className="cat-main" onClick={() => focusNextOfType(t)}>
              <span className="sw" />
              <span>{CATEGORIES[t].l}</span>
              {o.unseen > 0 && <span className="dot" style={DOT} />}
              <span className="n num">{o.redacted}</span>
              {o.kept > 0 && <span className="kept">保留 {o.kept}</span>}
            </button>
            <button type="button" className="sel sm" aria-label={CATEGORIES[t].l + " 处理方式"} onClick={(e) => openCategoryMenu(e.currentTarget, t)}>
              {treatmentOf(single, t) === "ph" ? "占位" : "涂抹"}
              <Icon name="chev-d" size={12} />
            </button>
          </div>
        );
      })}
    </div>
  );
}

function Footer({ single, batch, undoCount }: { single: Single; batch: Batch | null; undoCount: number }) {
  const inBatch = single.from === "batch";
  const file = inBatch && batch ? batch.files[single.bfi!] : null;
  const exported = !!file && ["done", "partial"].includes(file.st);
  if (inBatch) {
    return (
      <div className="side-foot">
        {exported && (
          <div className="side-note">
            <Icon name="info" size={13} />
            已导出 · 修改后将重新导出并替代旧版本
          </div>
        )}
        <Button variant="primary" large wide onClick={returnToBatch}>
          {exported && undoCount ? "完成并重新导出" : "完成"}
        </Button>
      </div>
    );
  }
  return (
    <div className="side-foot">
      <Button variant="primary" large wide onClick={exportSingle}>
        {isPartialResult(single) ? "导出部分结果" : "确认并导出"}
      </Button>
      <button type="button" className="out" aria-label="导出位置" onClick={(e) => openOutputMenu(e.currentTarget)} {...tip("导出位置", { placement: "above" })}>
        <Icon name="folder" size={13} />
        <span>{single.out}</span>
        <Icon name="chev-d" size={12} />
      </button>
    </div>
  );
}

/** The right-hand column of the review: file, settings, hits by category and the export button. */
export function ReviewSidebar({ single, undoCount }: { single: Single; undoCount: number }) {
  const batch = useAppState((s) => s.batch);
  return (
    <aside className="side">
      <div className="side-head">
        <FileIcon ext={single.ext} />
        <div style={{ minWidth: 0 }}>
          <div className="fn">{single.name}</div>
          <div className="fm">
            {single.live ? single.ext.toUpperCase() + " · " + single.pages.length + " 页" : single.pages.length + " 页 · " + single.size}
          </div>
        </div>
      </div>
      <Summary single={single} batch={batch} />
      <Categories single={single} />
      <Footer single={single} batch={batch} undoCount={undoCount} />
    </aside>
  );
}
