import { setPresetTab } from "../../actions/presets";
import { useAppState } from "../../store";
import { Page, Tabs } from "../../ui";
import { RulesEditor } from "./RulesEditor";
import { SchemeEditor } from "./SchemeEditor";

export function PresetsPage() {
  const tab = useAppState((s) => s.presetTab);
  return (
    <Page className="presets-page">
      <Tabs flush value={tab} onChange={setPresetTab} tabs={[{ value: "scheme", label: "方案" }, { value: "rules", label: "规则" }]} />
      {tab === "rules" ? <RulesEditor /> : <SchemeEditor />}
    </Page>
  );
}
