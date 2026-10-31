import { setHistoryPhase } from "../../actions/home";
import { singleManifest } from "../../domain/single";
import type { BatchSummary, Single } from "../../domain/types";
import { useAppState } from "../../store";
import { ManifestPage } from "../batch/ManifestPage";
import { ResultPage } from "../batch/ResultPage";
import { SingleDone } from "../single/SingleDone";

/** The result of a finished task opened from “最近”. `data` is null for the batch that is still in its slot. */
export function HistoryResultPage() {
  const result = useAppState((s) => s.historyResult);
  const live = useAppState((s) => s.batch);
  if (!result) return null;
  const onManifest = () => setHistoryPhase("manifest");
  if (result.entry.k === "file") {
    const single = result.data as Single;
    return result.phase === "manifest" ? <ManifestPage summary={singleManifest(single)} /> : <SingleDone single={single} onManifest={onManifest} />;
  }
  const batch = (result.data ?? live) as BatchSummary | null;
  if (!batch) return null;
  return result.phase === "manifest" ? <ManifestPage summary={batch} /> : <ResultPage batch={batch} onManifest={onManifest} />;
}
