import { useAppState } from "../../store";
import { openConfidenceMenu, openPresetModelMenu, openPresetMoreMenu, openPresetRulesMenu, copyPreset, newPreset, selectPreset, setCategoryTreatment, setDetection, startImport } from "../../actions/presets";
import { MODEL_LABELS } from "../../domain/catalog";
import { categoryName } from "../../domain/categories";
import {
  availableRulesets, categorySourceLabel, confidenceOf, effectiveTreatment, formatConfidence, modelById, presetConfidence, presetCategorySources, presetTreatment, schemeCategories,
  type CategorySource,
} from "../../domain/presets";
import { openModal } from "../../overlay/store";
import type { Preset, PresetTreatment } from "../../domain/types";
import { Icon, List, Pill, Row, SelectButton, Segmented, Swatch, tip } from "../../ui";
import { ConfigHeader } from "./ConfigHeader";
import { ItemList } from "./ItemList";

const TREATMENT_OPTIONS = [
  { value: "mask", label: "涂抹" },
  { value: "ph", label: "占位" },
  { value: "off", label: "关闭" },
] as const;

function ConfidenceRow({ preset: p, locked }: { preset: Preset; locked: boolean }) {
  const models = useAppState((s) => s.models);
  const c = confidenceOf(modelById(models, p.model));
  const v = presetConfidence(p, models);
  return (
    <div className="row" style={{ flexWrap: "wrap", rowGap: 8 }}>
      <span className="ri">
        <Icon name="sliders" size={17} />
      </span>
      <div style={{ minWidth: 0, flex: "1 1 140px" }}>
        <div className="rl">最低置信度</div>
        <div className="rs">低于此值的模型命中不做涂抹</div>
      </div>
      <span className="rv">
        {v > c.rec && (
          <Pill tone="warn" tabIndex={0} {...tip("高于推荐值，可能漏掉更多敏感信息；模型识别的类别不能无人值守处理", { align: "end" })}>
            <Icon name="alert" size={12} />
            可能漏检
          </Pill>
        )}
        <SelectButton disabled={locked} chevron={!locked} onClick={(e) => openConfidenceMenu(e.currentTarget)}>
          {formatConfidence(v)}
          <span className="muted sm">{v === c.rec ? "推荐" : "推荐 " + formatConfidence(c.rec)}</span>
        </SelectButton>
      </span>
    </div>
  );
}

const NO_SOURCE_HINT = "所选模型的标签与规则组的规则都不识别此类别，只能关闭";

/**
 * One category of the scheme: what detects it (the model, its rules, or both) and what is done with it. A
 * category nothing detects is off and cannot be switched on; a setting the scheme holds for it stays stored and
 * applies again once a source comes back (`kept`).
 */
function CategoryRow({ category, source, value, kept, locked }: { category: string; source?: CategorySource; value: PresetTreatment; kept: boolean; locked: boolean }) {
  const name = categoryName(useAppState((s) => s.categories), category);
  const detected = !!source;
  return (
    <div className={detected ? "row" : "row row-nosource"} data-category={category}>
      <Swatch category={category} />
      <span className="rl">{name}</span>
      {/* which detector covers the category, so switching models shows what the model itself finds */}
      {detected ? <span className={source.model ? "tag tp" : "tag"} data-source>{categorySourceLabel(source)}</span>
        : <span className="tag tag-none" data-source tabIndex={0} {...tip(NO_SOURCE_HINT + (kept ? "；已保存的设置会在可识别后生效" : ""))}>不识别</span>}
      <span className="rv" {...(detected ? {} : tip(NO_SOURCE_HINT, { align: "end" }))}>
        <Segmented value={value === "na" ? "off" : value} disabled={locked || !detected} onChange={(k) => setCategoryTreatment(category, k)} options={[...TREATMENT_OPTIONS]} />
      </span>
    </div>
  );
}

/** Schemes: the list on the left, the selected scheme's settings on the right. */
export function SchemeEditor() {
  const presets = useAppState((s) => s.presets);
  const models = useAppState((s) => s.models);
  const rulesets = useAppState((s) => s.rulesets);
  const plugins = useAppState((s) => s.plugins);
  const catalog = useAppState((s) => s.categories);
  const selected = useAppState((s) => s.presetSel);
  const p = presets.find((x) => x.id === selected) || presets[0]!;
  const locked = !!p.builtin;
  const model = modelById(models, p.model);
  const available = availableRulesets(rulesets, plugins);
  const sets = available.filter((rs) => p.rules.includes(rs.id));
  const sources = presetCategorySources(p, models, available);
  return (
    <div className="split">
      <ItemList
        items={presets.map((x) => ({ id: x.id, icon: "layers", title: x.name, subtitle: x.ver + (x.edited ? " · " + x.edited : "") }))}
        selectedId={p.id}
        onSelect={selectPreset}
        onCreate={newPreset}
        onImport={() => startImport("preset")}
      />
      <div className="preset-detail" role="region" aria-label="方案内容" tabIndex={0}>
        <ConfigHeader name={p.name} version={p.ver} locked={locked} lockedHint="内建方案，复制后可编辑" onTest={() => openModal({ type: "presetTest" })} onCopy={copyPreset} onMore={openPresetMoreMenu} />
        <List style={{ marginBottom: 14 }}>
          <Row icon="scan" title="检测方式">
            <Segmented
              value={p.det}
              disabled={locked}
              onChange={setDetection}
              options={[{ value: "model", label: "模型 + 规则" }, { value: "rules", label: "仅规则" }]}
            />
          </Row>
          {p.det === "model" && (
            <Row icon="cpu" title="模型">
              <SelectButton disabled={locked} chevron={!locked} onClick={(e) => openPresetModelMenu(e.currentTarget)}>
                {model?.name || "模型不可用"}
                <span className="muted sm">
                  {model?.version || ""} · {(model && MODEL_LABELS[model.status]) || "不可用"}
                </span>
              </SelectButton>
            </Row>
          )}
          {p.det === "model" && <ConfidenceRow preset={p} locked={locked} />}
          <Row icon="hash" title="规则组">
            <SelectButton disabled={locked} chevron={!locked} onClick={(e) => openPresetRulesMenu(e.currentTarget)}>
              {sets.length ? sets.map((x) => x.name).join(" · ") : <span style={{ color: "var(--bad)" }}>未选择</span>}
            </SelectButton>
          </Row>
          <Row icon="image" title="图像与嵌入">
            <span className="muted" style={{ paddingRight: 10 }}>
              涂抹
            </span>
          </Row>
        </List>
        <List>
          {schemeCategories(p, models, available, catalog).map((t) => (
            <CategoryRow key={t} category={t} source={sources.get(t)} value={effectiveTreatment(p, t, sources)} kept={["mask", "ph"].includes(presetTreatment(p, t))} locked={locked} />
          ))}
        </List>
      </div>
    </div>
  );
}
