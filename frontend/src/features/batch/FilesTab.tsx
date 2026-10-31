import { memo, useMemo, useState } from "react";
import { openBatchFile, setFileFilter, setFileQuery, showMoreFiles } from "../../actions/batch";
import { FILE_STATUS } from "../../domain/catalog";
import { matchesFileFilter } from "../../domain/batch";
import { fmt } from "../../domain/format";
import type { Batch, BatchFile, FileFilter } from "../../domain/types";
import { Empty, FilterChips, Icon, LinkButton, Pill, SearchBox } from "../../ui";
import type { PillTone } from "../../ui";

const FILTERS: { value: FileFilter; label: string }[] = [
  { value: "all", label: "全部" }, { value: "done", label: "已导出" }, { value: "run", label: "进行中" }, { value: "wait", label: "待你处理" }, { value: "bad", label: "未导出" },
];
const PAGE = 60;

/** Search box and filter chips above the file table. */
export function FilesToolbar({ batch: B }: { batch: Batch }) {
  // The box keeps what was typed; the batch keeps the trimmed query.
  const [text, setText] = useState(B.q);
  const counts = useMemo(
    () => FILTERS.map((f) => ({ ...f, count: B.files.filter((x) => matchesFileFilter(x, f.value, B.q)).length })),
    [B.files, B.q],
  );
  return (
    <div className="ftools">
      <SearchBox value={text} label="搜索文件" onChange={(v) => { setText(v); setFileQuery(v); }} />
      <span id="fchips">
        <FilterChips options={counts} value={B.ff} onChange={setFileFilter} />
      </span>
    </div>
  );
}

const FileRow = memo(function FileRow({ file: f }: { file: BatchFile }) {
  const [label, tone] = FILE_STATUS[f.st];
  const hits = Object.values(f.h).reduce((a, b) => a + b, 0);
  return (
    <tr className="cl" onClick={() => openBatchFile(f.i)}>
      <td className="mono muted">{f.id}</td>
      <td className="nm">{f.name}</td>
      <td className="r num muted">{fmt(f.pages)}</td>
      <td className="r num">{f.det && f.st !== "bad" ? fmt(hits) : <span className="muted">—</span>}</td>
      <td>
        <Pill tone={(tone || undefined) as PillTone | undefined} dot={f.st === "proc"}>
          {label}
          {f.reason ? " · " + f.reason : ""}
        </Pill>
      </td>
      <td className="r" style={{ color: "var(--faint)" }}>
        <Icon name="chev-r" size={15} />
      </td>
    </tr>
  );
});

/** Files of the batch, paged: the table never renders all of them (AR-11). */
export function FilesTab({ batch: B }: { batch: Batch }) {
  const rows = useMemo(() => B.files.filter((f) => matchesFileFilter(f, B.ff, B.q)), [B.files, B.ff, B.q]);
  const shown = rows.slice(0, B.lim || PAGE);
  return (
    <>
      <table className="tbl">
        <thead>
          <tr>
            <th>编号</th>
            <th>文件</th>
            <th className="r">页</th>
            <th className="r">命中</th>
            <th>状态</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {shown.map((f) => (
            <FileRow key={f.i} file={f} />
          ))}
        </tbody>
      </table>
      {rows.length > shown.length && (
        <div className="more">
          <LinkButton onClick={showMoreFiles}>
            再显示 {Math.min(PAGE, rows.length - shown.length)} · 共 {fmt(rows.length)}
          </LinkButton>
        </div>
      )}
      {!rows.length && <Empty>没有匹配的文件</Empty>}
    </>
  );
}
