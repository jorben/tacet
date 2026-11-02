import type { ReactNode } from "react";
import { DOCUMENT_TITLES } from "../../domain/catalog";
import { reviewDocumentData } from "../../domain/review";
import type { BatchFile, ExceptionGroup, ReviewItem } from "../../domain/types";
import { Icon, categoryColor } from "../../ui";
import type { CategoryId } from "../../domain/types";

function CurveText() {
  return (
    <svg width="150" height="30" aria-hidden="true">
      <text x="0" y="25" fontSize="25" fontWeight="700" fontFamily="Songti SC,SimSun,serif" fill="none" stroke="#2b2b2b" strokeWidth=".8">
        澄音科技
      </text>
    </svg>
  );
}

interface ReviewPaperProps {
  group: ExceptionGroup;
  file: BatchFile;
  item: ReviewItem;
  /** The original, or what the output would look like if `decision` were taken. */
  view: "orig" | "fx";
  decision?: string;
}

/**
 * A synthetic page that stands in for the document an exception points into. The element the decision is about
 * carries `tgt`; the effect view shows it blocked out when the decision is “涂抹”.
 */
export function ReviewPaper({ group: g, file: f, item: it, view, decision }: ReviewPaperProps) {
  const d = reviewDocumentData(f, it);
  const original = view === "orig";
  const blocked = !original && decision === "do";
  const field = (t: CategoryId, v: string) =>
    original ? (
      <span className="hit" style={categoryColor(t)}>
        {v}
      </span>
    ) : (
      <span className="rd" style={categoryColor(t)}>
        {v}
      </span>
    );
  const line = (label: string, value: ReactNode) => (
    <p>
      {label}：{value}
    </p>
  );
  const paras = (from: number, n: number) => d.paras.slice(from, from + n).map((x, i) => <p className="ind" key={from + i}>{x}</p>);
  const head = (
    <>
      <div className="d-title" style={{ fontSize: 20, letterSpacing: ".25em" }}>
        {DOCUMENT_TITLES[d.kind]}
      </div>
      <div className="d-meta">
        编号：{f.id}-{100 + it.j}
      </div>
    </>
  );
  const base = (
    <>
      {line("单位", field("org", d.org))}
      {line("姓名", field("name", d.name))}
      {line("联系电话", field("phone", d.phone))}
      {line("电子邮箱", field("email", d.email))}
    </>
  );
  let body: ReactNode = null;
  let cls = "";
  switch (g.id) {
    case "idck":
      body = (
        <>
          {head}
          {base}
          {line(
            "证件号码",
            original ? (
              <span className="hit tgt" style={categoryColor("id_number")}>
                {d.id}
              </span>
            ) : blocked ? (
              <span className="rd tgt" style={categoryColor("id_number")}>
                {d.id}
              </span>
            ) : (
              <span className="tgt keepv">{d.id}</span>
            ),
          )}
          {line("现住址", field("addr", d.addr))}
          {paras(0, 3)}
        </>
      );
      break;
    case "shot":
      body = (
        <>
          {head}
          {base}
          {paras(0, 1)}
          {blocked ? (
            <div className="shot-gone tgt">截图已涂抹</div>
          ) : (
            <div className="shot tgt">
              <div className="bar">
                <i></i>
                <i></i>
                <i></i>
              </div>
              <div className="bd">
                <span className="bb">您好，我是{d.callee}，回访电话 {d.cphone}</span>
                <span className="bb me">收到，合同已发您邮箱</span>
                <span className="bb">好的，身份证复印件稍后补</span>
              </div>
            </div>
          )}
          <div className="cap">图 {it.j + 1}　沟通记录截图</div>
          {paras(1, 2)}
        </>
      );
      break;
    case "embed":
      body = (
        <>
          {head}
          {base}
          {paras(0, 2)}
          {blocked ? (
            <div className="att gone2 tgt">
              <Icon name="clip" size={18} />
              <span>附件已涂抹</span>
            </div>
          ) : (
            <div className="att tgt">
              <Icon name="clip" size={18} />
              <span>
                {d.att}
                <small>{d.kb} KB · 嵌入对象</small>
              </span>
            </div>
          )}
          {paras(2, 2)}
        </>
      );
      break;
    case "curve":
      body = (
        <>
          <div className="lh tgt">
            {blocked ? <span className="rd" style={{ display: "inline-block", width: 150, height: 30 }}></span> : <CurveText />}
            <span style={{ fontSize: 11, color: "#888" }}>内部资料</span>
          </div>
          {head}
          {base}
          {paras(0, 3)}
        </>
      );
      break;
    case "scan":
      cls = "scan tgt";
      body = (
        <>
          {d.scan.map((w, i) => (
            <div key={i} className="scanl" style={{ width: (i % 6 === 0 ? 40 : w) + "%", ...(i === 0 ? { margin: "6px auto 26px", height: 14 } : {}) }}></div>
          ))}
          {blocked && <div className="scan-gone">整页已涂抹</div>}
        </>
      );
      break;
  }
  const pageNo = f.ext !== "docx" ? (g.id === "scan" ? 1 : it.p) + " / " + f.pages : f.i % 4 ? "约 " + it.p + " / " + f.pages : "";
  return (
    <div className={"paper pv " + cls}>
      {body}
      <div className="pgno">{pageNo}</div>
    </div>
  );
}
