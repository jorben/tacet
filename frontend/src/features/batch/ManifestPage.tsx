import { memo, useMemo } from "react";
import { setManifestFilter, showMoreManifest } from "../../actions/batch";
import { revealOutput, saveFile } from "../../actions/single";
import { countFiles, exportedCount, fakeHash, isExported, manifestRows } from "../../domain/batch";
import { FILE_STATUS } from "../../domain/catalog";
import { fmt } from "../../domain/format";
import type { BatchSummary, ManifestFile, ManifestFilter } from "../../domain/types";
import { Button, FilterChips, LinkButton, Page, Pill, Tag } from "../../ui";
import type { PillTone } from "../../ui";
import { ExportedCount } from "./BatchProgress";

const PAGE = 80;

const ManifestRow = memo(function ManifestRow({ file: f, groups }: { file: ManifestFile; groups: BatchSummary["groups"] }) {
  const [label, tone] = FILE_STATUS[f.st];
  const ok = isExported(f.st);
  const waiting = f.st === "wait" || f.st === "held";
  return (
    <tr>
      <td className="mono">{f.id}</td>
      <td className="mono">
        {ok ? (
          <>
            {f.outputName ?? f.id + "." + (f.ext === "pages" ? "pdf" : f.ext)}
            {(f.ver ?? 0) > 1 && (
              <>
                {" "}
                <Tag>v{f.ver} · 替代旧版</Tag>
              </>
            )}
          </>
        ) : (
          <span className="muted">—</span>
        )}
      </td>
      <td>
        <Pill tone={(tone || undefined) as PillTone | undefined}>
          {label}
          {f.reason ? " · " + f.reason : ""}
          {waiting ? " · " + (f.g || []).map((x) => groups?.find((g) => g.id === x)?.t).join("、") : ""}
        </Pill>
      </td>
      <td className="muted">{ok ? (f.man ? "自动 + 人工" : "自动") : ""}</td>
      <td className="mono muted">{ok ? fakeHash(f.i) : ""}</td>
    </tr>
  );
});

/** The delivery list of a batch or a single file: every output with its status and checksum. */
export function ManifestPage({ summary }: { summary: BatchSummary }) {
  const c = countFiles(summary.files);
  const exported = exportedCount(c);
  const filter: ManifestFilter = summary.manifestFilter || "all";
  const rows = useMemo(() => manifestRows(summary), [summary]);
  const shown = rows.slice(0, summary.manifestLimit || PAGE);
  return (
    <Page id="mpage">
      <div className="bhead">
        <ExportedCount exported={exported} total={summary.n} caption={"已导出" + (c.partial ? " · 含部分结果 " + c.partial : "")} />
        <span className="sp" />
        <Button icon="folder" onClick={revealOutput}>
          导出文件夹
        </Button>
        <Button icon="download" style={{ marginLeft: 6 }} onClick={() => saveFile("manifest.json")}>
          保存清单
        </Button>
      </div>
      <div className="ftools" style={{ marginTop: 22 }}>
        <FilterChips
          value={filter}
          onChange={setManifestFilter}
          options={[
            { value: "all", label: "全部", count: summary.n },
            { value: "ok", label: "已导出", count: exported },
            { value: "no", label: "未导出", count: summary.n - exported },
          ]}
        />
      </div>
      <table className="tbl">
        <thead>
          <tr>
            <th>编号</th>
            <th>导出</th>
            <th>状态</th>
            <th>处理</th>
            <th>SHA-256</th>
          </tr>
        </thead>
        <tbody>
          {shown.map((f) => (
            <ManifestRow key={f.i} file={f} groups={summary.groups} />
          ))}
        </tbody>
      </table>
      {rows.length > shown.length && (
        <div className="more">
          <LinkButton onClick={showMoreManifest}>再显示 {PAGE} · 共 {fmt(rows.length)}</LinkButton>
        </div>
      )}
    </Page>
  );
}
