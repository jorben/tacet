import { setPresetTab } from "../../actions/presets";
import { useAppState } from "../../store";
import { Page, Tabs } from "../../ui";
import { ModelMappingEditor } from "./ModelMappingEditor";
import { RulesEditor } from "./RulesEditor";
import { SchemeEditor } from "./SchemeEditor";

export function PresetsPage() {
  const tab = useAppState((s) => s.presetTab);
  return (
    <Page className="presets-page">
      <Tabs flush value={tab} onChange={setPresetTab} tabs={[{ value: "scheme", label: "方案" }, { value: "rules", label: "规则" }, { value: "models", label: "模型" }]} />
      {tab === "rules" ? <RulesEditor /> : tab === "models" ? <ModelMappingEditor /> : <SchemeEditor />}
    </Page>
  );
}
