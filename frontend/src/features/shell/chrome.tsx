// What the title bar says and where “返回” leads, derived from the application state.
import type { ReactNode } from "react";
import { goBack, goTo } from "../../actions/navigation";
import { setHistoryPhase } from "../../actions/home";
import { setBatchPhase } from "../../actions/batch";
import { returnToBatch, setSinglePhase } from "../../actions/single";
import { machineDone } from "../../domain/batch";
import { fmt } from "../../domain/format";
import type { AppState } from "../../store";
import { Logo } from "../../ui";

/** The parts of the state the title bar depends on. */
export type ChromeState = Pick<AppState, "view" | "single" | "batch" | "historyResult" | "navStack">;

const PAGE_TITLES: Partial<Record<AppState["view"], string>> = { presets: "方案", plugins: "插件", settings: "设置" };

export function windowTitle(s: ChromeState): ReactNode {
  const { view, single, batch, historyResult } = s;
  if (view === "history" && historyResult) return <span>{historyResult.phase === "manifest" ? "导出清单" : historyResult.entry.name}</span>;
  if (view === "single" && single) return <span>{single.phase === "done" && single.resultPhase === "manifest" ? "导出清单" : single.name}</span>;
  if (view === "batch" && batch) {
    const b = batch;
    return <span>{b.phase === "manifest" ? "导出清单" : b.phase === "review" ? "人工复核" : b.phase === "result" ? b.title : "批次 · " + fmt(b.n) + " 份"}</span>;
  }
  return (
    PAGE_TITLES[view] || (
      <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <Logo size={16} />
        Tacet
      </span>
    )
  );
}

export interface BackTarget {
  label: string;
  go: () => void;
}

const PAGE_NAMES: Record<string, string> = { home: "任务", plugins: "插件", settings: "设置" };

export function backTarget(s: ChromeState): BackTarget | null {
  const { view, single, batch, historyResult, navStack } = s;
  if (view === "history" && historyResult) {
    return historyResult.phase === "manifest" ? { label: "返回结果", go: () => setHistoryPhase("result") } : { label: "返回任务", go: () => goTo("home") };
  }
  if (view === "single" && single?.phase === "done" && single.resultPhase === "manifest") return { label: "返回结果", go: () => setSinglePhase("result") };
  if (navStack.length) {
    const r = navStack[navStack.length - 1]!;
    const where = r.view === "single" && single ? single.name : r.view === "presets" ? (r.presetTab === "rules" ? "规则" : "方案") : PAGE_NAMES[r.view] || "上一页";
    return { label: "返回 " + where, go: goBack };
  }
  if (view === "single" && single?.from === "batch") return { label: "返回批次", go: returnToBatch };
  if (view === "batch" && batch && ["manifest", "review"].includes(batch.phase)) {
    const done = machineDone(batch);
    return { label: done ? "返回结果" : "返回批次", go: () => setBatchPhase("run") };
  }
  if (view === "single" || view === "batch") return { label: "返回任务", go: () => goTo("home") };
  return null;
}
