import { setSinglePhase } from "../../actions/single";
import { singleManifest } from "../../domain/single";
import { useAppState } from "../../store";
import { ManifestPage } from "../batch/ManifestPage";
import { ProcessingCard } from "./ProcessingCard";
import { SingleDone } from "./SingleDone";
import { SingleReview } from "./SingleReview";

/** The single-file task, whatever phase it is in. */
export function SinglePage() {
  const single = useAppState((s) => s.single);
  if (!single) return null;
  if (single.phase === "processing") return <ProcessingCard single={single} />;
  if (single.phase === "done") {
    return single.resultPhase === "manifest" ? <ManifestPage summary={singleManifest(single)} /> : <SingleDone single={single} onManifest={() => setSinglePhase("manifest")} />;
  }
  return <SingleReview single={single} />;
}
