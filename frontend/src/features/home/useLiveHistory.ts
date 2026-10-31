import { useMemo } from "react";
import { useAppState } from "../../store";
import { liveHistory } from "../../store/selectors";

/** Running tasks followed by the saved ones, as listed under “最近”. */
export function useLiveHistory() {
  const batch = useAppState((s) => s.batch);
  const single = useAppState((s) => s.single);
  const behind = useAppState((s) => s.behind);
  const history = useAppState((s) => s.history);
  return useMemo(() => liveHistory({ batch, single, behind, history }), [batch, single, behind, history]);
}
