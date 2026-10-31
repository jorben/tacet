import { openStopMenu, setBatchTab, togglePause } from "../../actions/batch";
import { countFiles, etaLabel, exportedCount, pendingItems } from "../../domain/batch";
import { fmt } from "../../domain/format";
import type { Batch, BatchTab } from "../../domain/types";
import { IconButton, Page, Tabs } from "../../ui";
import { AutoTab } from "./AutoTab";
import { BatchProgress, ExportedCount } from "./BatchProgress";
import { FilesTab, FilesToolbar } from "./FilesTab";
import { TodoTab } from "./TodoTab";

/** A batch that is running: progress, what needs the user, what was done automatically, and the files. */
export function RunPage({ batch: B }: { batch: Batch }) {
  const c = countFiles(B.files);
  const pending = pendingItems(B);
  const tabs: { value: BatchTab; label: React.ReactNode }[] = [
    {
      value: "todo",
      label: (
        <>
          待你处理
          <span id="tbadge">{pending ? <span className="badge">{fmt(pending)}</span> : null}</span>
        </>
      ),
    },
    { value: "auto", label: "自动处理" },
    { value: "files", label: "文件" },
  ];
  return (
    <Page id="bpage">
      <div className="bhead">
        <div id="bbig">
          <ExportedCount exported={exportedCount(c)} total={B.n} caption="已导出" />
        </div>
        <span className="sp" />
        <span className="eta" id="beta">
          {etaLabel(c)}
        </span>
        <IconButton icon={B.paused ? "play" : "pause"} size={17} label={B.paused ? "继续" : "暂停"} tooltip={B.paused ? "继续" : "暂停派发"} onClick={togglePause} />
        <IconButton icon="stop" size={17} label="停止" tooltip="停止" onClick={(e) => openStopMenu(e.currentTarget)} />
      </div>
      <BatchProgress batch={B} />
      <Tabs tabs={tabs} value={B.tab} onChange={setBatchTab} />
      {B.tab === "files" && <FilesToolbar batch={B} />}
      <div id="btab">{B.tab === "todo" ? <TodoTab batch={B} /> : B.tab === "auto" ? <AutoTab batch={B} /> : <FilesTab batch={B} />}</div>
    </Page>
  );
}
