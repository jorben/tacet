import type { RefObject } from "react";
import { previewReviewDecision, setReviewAll, stepReview } from "../../actions/review";
import { REVIEW_SOURCE } from "../../domain/catalog";
import { reviewWhere } from "../../domain/review";
import type { Batch, ReviewItem } from "../../domain/types";
import { Button, Field, Icon, KeyValue, tip } from "../../ui";

interface ReviewPanelProps {
  batch: Batch;
  item: ReviewItem;
  onDecide: (k: string) => void;
  pwdRef: RefObject<HTMLInputElement>;
}

/** The decision for the current item: what it is, the choices (with keyboard keys A/B), and progress. */
export function ReviewPanel({ batch: B, item: it, onDecide, pwdRef }: ReviewPanelProps) {
  const rv = B.rv!;
  const g = B.groups[it.gi]!;
  const f = B.files[it.fi]!;
  const items = rv.items.filter((x) => x.gid === it.gid);
  const rest = items.filter((x) => !x.dec && x !== it).length;
  const decided = rv.items.filter((x) => x.dec).length;
  const total = rv.items.length;
  return (
    <aside className="rvp">
      <div className="rvp-h">
        <div className={g.block ? "gi2 block" : "gi2"}>
          <Icon name={g.ic} size={17} />
        </div>
        <div style={{ minWidth: 0, flex: 1 }}>
          <div className="t">{g.t}</div>
          <div className="sm muted num" id="rvIdx">
            {items.indexOf(it) + 1} / {items.length}
          </div>
        </div>
      </div>
      <div className="rvp-b">
        <KeyValue
          items={[
            ["文件", f.name],
            ["位置", reviewWhere(g, f, it)],
            ["依据", (g.id === "fail" ? f.failWhy || "" : REVIEW_SOURCE[g.id]) + (g.found?.[it.fi] ? " · 导出后验证发现" : "")],
          ]}
        />
        <div className="ask">{it.dec ? "已决策 · 可更改" : "请决策"}</div>
        {g.id === "pwd" && it.dec !== "pwd" && (
          <Field key={it.key} ref={pwdRef} labelStyle={{ margin: "0 0 8px" }} icon="lock" type="password" id="rvPwd" placeholder="输入密码后选 A" aria-label="密码" />
        )}
        <div className="acts-v">
          {g.acts.map((a, i) => {
            const selected = it.dec === a.k;
            return (
              <button
                key={a.k}
                type="button"
                className={selected ? "dopt on" : "dopt"}
                aria-pressed={selected}
                onMouseEnter={() => previewReviewDecision(a.k)}
                onMouseLeave={() => previewReviewDecision(null)}
                onFocus={() => previewReviewDecision(a.k)}
                onClick={() => onDecide(a.k)}
              >
                <span className="key">{"AB"[i]}</span>
                <span>{a.l}</span>
                {a.n && <span className="dn2">{a.n}</span>}
                {selected && (
                  <span className="ck">
                    <Icon name="check" size={16} strokeWidth={2.4} />
                  </span>
                )}
              </button>
            );
          })}
        </div>
        {!it.dec && rest > 0 && g.id !== "pwd" && !g.noBulk && (
          <label className="rest">
            <input type="checkbox" id="rvAll" checked={rv.all} onChange={(e) => setReviewAll(e.target.checked)} /> 本组其余 <span id="rvRest" className="num">{rest}</span> {g.unit}一起
          </label>
        )}
        <div className="navs">
          <Button {...tip("↑", { placement: "above" })} onClick={() => stepReview(-1)}>
            <Icon name="chev-l" size={15} />
            上一条
          </Button>
          <Button {...tip("↓", { placement: "above" })} onClick={() => stepReview(1)}>
            下一条
            <Icon name="chev-r" size={15} />
          </Button>
        </div>
      </div>
      <div className="rvp-f">
        <div id="rvpf">
          <div className="sm" style={{ display: "flex" }}>
            <span className="muted">已决定</span>
            <span className="sp" />
            <span className="num">
              {decided} / {total}
            </span>
          </div>
          <div className="mini">
            <i style={{ width: (total ? (decided / total) * 100 : 0) + "%" }}></i>
          </div>
        </div>
      </div>
    </aside>
  );
}
