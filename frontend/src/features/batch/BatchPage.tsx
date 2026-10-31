import { setBatchPhase } from "../../actions/batch";
import { useAppState } from "../../store";
import { ReviewPage } from "../review/ReviewPage";
import { ManifestPage } from "./ManifestPage";
import { PrecheckPage } from "./PrecheckPage";
import { ResultPage } from "./ResultPage";
import { RunPage } from "./RunPage";

/** The batch task, whatever phase it is in. */
export function BatchPage() {
  const batch = useAppState((s) => s.batch);
  if (!batch) return null;
  switch (batch.phase) {
    case "precheck": return <PrecheckPage batch={batch} />;
    case "manifest": return <ManifestPage summary={batch} />;
    case "result": return <ResultPage batch={batch} onManifest={() => setBatchPhase("manifest")} />;
    case "review": return <ReviewPage batch={batch} />;
    default: return <RunPage batch={batch} />;
  }
}
