import { openHistoryEntry } from "../../actions/home";
import type { HistoryEntry, HistoryStatus } from "../../domain/types";
import { FileIcon, Icon, Pill, type PillTone } from "../../ui";

const TONE: Record<HistoryStatus, PillTone | undefined> = { done: "ok", warn: "warn", run: "pri", keep: "ghost", idle: "pri", stop: "ghost", part: "ghost" };

/** One recent task: what it is, how far it got, and a way to open it. */
export function HistoryRow({ entry }: { entry: HistoryEntry }) {
  return (
    <button type="button" className="row hrow" onClick={() => openHistoryEntry(entry.id)}>
      {entry.k === "batch" ? (
        <span className="ficon">
          <Icon name="layers" size={25} />
        </span>
      ) : (
        <FileIcon ext={entry.ext || "pdf"} />
      )}
      <span className="hx">
        <span className="hn" style={{ display: "block" }}>
          {entry.name}
        </span>
        <span className="rs" style={{ display: "block" }}>
          {entry.meta} · {entry.when}
        </span>
      </span>
      <Pill tone={TONE[entry.st] || "ghost"} dot={entry.st === "run"}>
        {entry.stl}
      </Pill>
      <Icon name="chev-r" size={18} className="history-chevron" />
    </button>
  );
}

export function HistoryRows({ entries }: { entries: HistoryEntry[] }) {
  return (
    <>
      {entries.map((e) => (
        <HistoryRow key={e.id} entry={e} />
      ))}
    </>
  );
}
