import { useAppState } from "../../store";
import { BatchPage } from "../batch/BatchPage";
import { HistoryResultPage } from "../history/HistoryResultPage";
import { HomePage } from "../home/HomePage";
import { ModelCard } from "../models/ModelCard";
import { PluginsPage } from "../plugins/PluginsPage";
import { PresetsPage } from "../presets/PresetsPage";
import { SettingsPage } from "../settings/SettingsPage";
import { SinglePage } from "../single/SinglePage";

/** The page for the current view. A waiting operation shows its model card on every page that is not about models. */
export function MainView() {
  const view = useAppState((s) => s.view);
  const waiting = useAppState((s) => !!s.modelPending);
  const precheck = useAppState((s) => s.batch?.phase === "precheck");
  const gate = waiting && view !== "home" && view !== "plugins" && !(view === "batch" && precheck);
  return (
    <main id="main">
      {gate && (
        <div className="model-gate">
          <ModelCard />
        </div>
      )}
      {view === "home" && <HomePage />}
      {view === "history" && <HistoryResultPage />}
      {view === "single" && <SinglePage />}
      {view === "batch" && <BatchPage />}
      {view === "presets" && <PresetsPage />}
      {view === "plugins" && <PluginsPage />}
      {view === "settings" && <SettingsPage />}
    </main>
  );
}
