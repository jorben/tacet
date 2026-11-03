import { useAppState } from "../../store";
import {
  openConfidenceMenu, openPresetModelMenu, openPresetMoreMenu, openPresetRulesMenu, copyPreset, newPreset, selectPreset, setCategoryTreatment, setDetection, showInConfig, startImport,
} from "../../actions/presets";
import { MODEL_LABELS } from "../../domain/catalog";
import {
  availableRulesets, confidenceOf, effectiveTreatment, formatConfidence, modelById, presetConfidence, presetCategorySources, rulesetDetecting, schemeCategories,
  type CategorySource,
} from "../../domain/presets";
import { openModal } from "../../overlay/store";
import type { Preset, PresetTreatment, Ruleset } from "../../domain/types";
import { Button, Icon, IconButton, List, Pill, Row, SectionTitle, SelectButton, Segmented, tip } from "../../ui";
import { CategoryRow } from "./CategoryRow";
import { ConfigHeader, ReadonlyNotice, RefLinks } from "./ConfigHeader";
import { CatalogNotice, GuideCard } from "./ConfigStates";
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

const KEPT_HINT = "方案存有对它的设置，可识别后会再次生效";

/**
 * A category something of the scheme detects: its sources, each a link to where it comes from (the model's labels,
 * the rule set's rules), and what is done with it.
 */
function DetectedRow({ preset: p, category, source, value, locked, rulesets }: { preset: Preset; category: string; source: CategorySource; value: PresetTreatment; locked: boolean; rulesets: Ruleset[] }) {
  const rules = source.rules ? rulesetDetecting(p, rulesets, category) : undefined;
  const tags = (
    <>
      {source.model && (
        <button type="button" className="tag tp tag-link" data-source="model" {...tip("在模型页查看计入此类别的标签", { align: "start" })} onClick={() => showInConfig({ tab: "models", id: p.model, category })}>模型</button>
      )}
      {rules && (
        <button type="button" className="tag tag-link" data-source="rules" {...tip("在规则页查看「" + rules.name + "」中的规则", { align: "start" })} onClick={() => showInConfig({ tab: "rules", id: rules.id, category })}>规则</button>
      )}
    </>
  );
  return (
    <CategoryRow category={category} tags={tags}>
      <Segmented value={value === "na" ? "off" : value} disabled={locked} onChange={(k) => setCategoryTreatment(category, k)} options={[...TREATMENT_OPTIONS]} />
    </CategoryRow>
  );
}

/**
 * The categories nothing of the scheme detects, folded under one heading that says why once. They are off and cannot
 * be switched on; a mask or placeholder the scheme holds for one (not the default an unsaved scheme shows, which is
 * not stored for it) stays stored and applies again once a source comes back (`kept`).
 */
function UndetectedGroup({ preset: p, categories }: { preset: Preset; categories: string[] }) {
  const why = p.det === "model" ? "所选模型的标签与规则组的规则都不识别，只能关闭" : "仅规则方案不用模型，规则组的规则不识别，只能关闭";
  return (
    <details className="cat-fold">
      <summary className="row cat-fold-head">
        <Icon name="chev-r" size={14} className="cat-fold-chev" />
        <span className="rl">不识别的类别（{categories.length}）</span>
        <span className="muted sm cat-fold-why">{why}</span>
      </summary>
      {categories.map((t) => (
        <CategoryRow
          key={t}
          category={t}
          className="row-nosource"
          tags={["mask", "ph"].includes(p.cats[t] ?? "") ? <span className="tag cat-kept" tabIndex={0} {...tip(KEPT_HINT, { align: "start" })}>已存设置</span> : undefined}
        >
          <span className="muted sm cat-off">关闭</span>
        </CategoryRow>
      ))}
    </details>
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
  const home = useAppState((s) => s.homePreset);
  const p = presets.find((x) => x.id === selected) ?? presets[0];
  const list = (
    <ItemList
      label="方案"
      items={presets.map((x) => ({ id: x.id, icon: "layers", title: x.name, subtitle: [x.ver, x.det === "model" ? "模型 + 规则" : "仅规则"].join(" · "), locked: !!x.builtin }))}
      selectedId={p?.id}
      onSelect={selectPreset}
      onCreate={newPreset}
      onImport={() => startImport("preset")}
    />
  );
  if (!p) {
    return (
      <div className="split">
        {list}
        <div className="preset-detail">
          <GuideCard icon="layers" title="还没有方案" actions={<><Button variant="primary" icon="plus" onClick={newPreset}>新建方案</Button><Button icon="folder" onClick={() => startImport("preset")}>导入</Button></>}>
            方案决定用哪个模型和规则组检测，以及每个类别怎样处理。
          </GuideCard>
        </div>
      </div>
    );
  }
  const locked = !!p.builtin;
  const model = modelById(models, p.model);
  const available = availableRulesets(rulesets, plugins);
  const sets = available.filter((rs) => p.rules.includes(rs.id));
  const sources = presetCategorySources(p, models, available);
  const categories = schemeCategories(p, models, available, catalog);
  const detected = categories.filter((t) => sources.has(t));
  const undetected = categories.filter((t) => !sources.has(t));
  const subtitle = (
    <>
      {home === p.name && "首页默认 · "}
      {p.det === "model" && <>模型 {model ? <RefLinks items={[model]} onOpen={(id) => showInConfig({ tab: "models", id })} /> : "不可用"} · </>}
      规则组 {sets.length ? <RefLinks items={sets} onOpen={(id) => showInConfig({ tab: "rules", id })} /> : "不可用"}
    </>
  );
  return (
    <div className="split">
      {list}
      <div className="preset-detail" role="region" aria-label="方案内容" tabIndex={0}>
        <ConfigHeader name={p.name} badge={p.ver} badgePrimary={!locked} subtitle={subtitle} notice={locked && <ReadonlyNotice onCopy={copyPreset}>内建方案，只读。</ReadonlyNotice>}>
          <Button icon="scan" onClick={() => openModal({ type: "presetTest" })}>测试</Button>
          <Button icon="copy" onClick={copyPreset}>复制</Button>
          <IconButton icon="more" label="更多" onClick={(e) => openPresetMoreMenu(e.currentTarget)} />
        </ConfigHeader>
        <CatalogNotice />
        <SectionTitle first>检测设置</SectionTitle>
        <List>
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
              {sets.length ? sets.map((x) => x.name).join(" · ")
                // the menu keeps at least one: none is left only when the chosen ones went away (deleted, plugin off)
                : <span className="warn-text" {...tip("方案选用的规则组已删除或所属插件已停用，请重新选择", { align: "end" })}>
                  <Icon name="alert" size={13} />所选规则组不可用
                </span>}
            </SelectButton>
          </Row>
        </List>
        <SectionTitle>
          类别处理
          <span className="sec-note">{detected.length} 个可设置</span>
        </SectionTitle>
        <List className="cat-list">
          {detected.map((t) => (
            <DetectedRow key={t} preset={p} category={t} source={sources.get(t)!} value={effectiveTreatment(p, t, sources)} locked={locked} rulesets={available} />
          ))}
          {!detected.length && <div className="row muted sm">所选模型与规则组都不识别任何类别</div>}
          {undetected.length > 0 && <UndetectedGroup key={p.id} preset={p} categories={undetected} />}
        </List>
        <SectionTitle>其他</SectionTitle>
        <List>
          <Row icon="image" title="图像与嵌入" description="固定涂抹，暂不支持修改" />
        </List>
      </div>
    </div>
  );
}
