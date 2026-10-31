import type { KeyboardEvent } from "react";
import { openGroupMenu } from "../../actions/batch";
import { startReview } from "../../actions/review";
import { countFiles, groupMembers, groupTotal, openGroups } from "../../domain/batch";
import { fmt } from "../../domain/format";
import type { Batch, ExceptionGroup } from "../../domain/types";
import { openModal } from "../../overlay/store";
import { Button, Empty, Icon, IconButton, SectionTitle } from "../../ui";

function ExceptionGroupRow({ batch, group: g }: { batch: Batch; group: ExceptionGroup }) {
  const mem = groupMembers(batch, g);
  const total = groupTotal(g, mem);
  const open = () => startReview(g.id);
  const onKey = (e: KeyboardEvent) => {
    if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      open();
    }
  };
  return (
    <div className={g.block ? "group go block" : "group go"} role="button" tabIndex={0} onClick={open} onKeyDown={onKey}>
      <div className="gi">
        <Icon name={g.ic} size={18} />
      </div>
      <div className="gx">
        <div className="gt">{g.t}</div>
        <div className="gm num">
          {g.unit === "份" ? fmt(mem.length) + " 份" : fmt(total) + " " + g.unit + " · " + fmt(mem.length) + " 份"}
          {g.found && mem.some((fi) => g.found![fi]) ? " · 含导出后发现" : ""}
        </div>
      </div>
      <div className="ga">
        {!g.noBulk && (
          <IconButton
            icon="more"
            label="整组处理"
            tooltip="整组处理"
            tooltipAlign="end"
            onClick={(e) => { e.stopPropagation(); openGroupMenu(e.currentTarget, g.id); }}
          />
        )}
        <span className="chev">
          <Icon name="chev-r" size={18} />
        </span>
      </div>
    </div>
  );
}

/** What waits for the user: exception groups, files to publish, and what has been decided. */
export function TodoTab({ batch: B }: { batch: Batch }) {
  const c = countFiles(B.files);
  const open = openGroups(B);
  const pending = open.reduce((a, g) => a + groupTotal(g, groupMembers(B, g)), 0);
  const files = new Set(open.flatMap((g) => groupMembers(B, g))).size;
  const awaitingPublish = !B.autoPub && c.ready > 0;
  const decided = B.groups.filter((g) => g.done.length);
  return (
    <>
      {open.length > 0 && (
        <div className="hero">
          <div>
            <div className="hn num">
              {fmt(pending)}
              <small>处待你处理</small>
            </div>
            <div className="hs num">
              {open.length} 类 · {fmt(files)} 份文件{B.mode === "auto" ? " · 已搁置，不阻塞其他文件" : ""}
            </div>
          </div>
          <span className="sp" />
          <Button variant="primary" large onClick={() => startReview(null)}>
            开始复核
          </Button>
        </div>
      )}
      {awaitingPublish && (
        <div className="banner info">
          <div>
            <div className="bt">待导出 {fmt(c.ready)} 份</div>
            <div className="bs">已通过验证，确认后写入导出位置</div>
          </div>
          <span className="sp" />
          <Button variant="primary" onClick={() => openModal({ type: "exportReady" })}>
            导出 {fmt(c.ready)} 份
          </Button>
        </div>
      )}
      {open.map((g) => (
        <ExceptionGroupRow key={g.id} batch={B} group={g} />
      ))}
      {!open.length && !awaitingPublish && <Empty mark>暂时没有需要你处理的</Empty>}
      {decided.length > 0 && (
        <>
          <SectionTitle>已处理</SectionTitle>
          {decided.map((g) => {
            const byLabel: Record<string, number> = {};
            g.done.forEach((d) => { byLabel[d.l] = (byLabel[d.l] || 0) + d.n; });
            return (
              <div className="group done" key={g.id}>
                <div className="gi">
                  <Icon name="check" size={15} strokeWidth={2.2} />
                </div>
                <div className="gx">
                  <div className="gt">{g.t}</div>
                </div>
                <div className="ga">
                  <span className="muted sm num">
                    {Object.entries(byLabel).map(([l, n]) => l + " " + fmt(n)).join(" · ")} {g.unit}
                  </span>
                </div>
              </div>
            );
          })}
        </>
      )}
    </>
  );
}
