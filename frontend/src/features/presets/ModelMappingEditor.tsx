import { useState } from "react";
import { deleteCategory } from "../../actions/categories";
import { resetModelMapping, setCategoryLabels, setLabelCategory } from "../../actions/modelMapping";
import { showModels } from "../../actions/models";
import { selectModel, showInConfig } from "../../actions/presets";
import { NewCategoryForm } from "../dialogs/NewCategoryForm";
import { byCatalogOrder, categoryName, listedCategories } from "../../domain/categories";
import { effectiveLabels, labelsOfCategory, unmappedLabels } from "../../domain/liveConfig";
import type { ModelPlugin } from "../../domain/types";
import { openMenu, openMultiMenu } from "../../overlay/store";
import { getState, useAppState } from "../../store";
import { Button, Icon, IconButton, List, SectionTitle, SelectButton, Tag, tip } from "../../ui";
import { CategoryRow, useCategoryFocus } from "./CategoryRow";
import { ConfigHeader, RefLinks } from "./ConfigHeader";
import { CatalogNotice, GuideCard } from "./ConfigStates";
import { ItemList } from "./ItemList";

const MAPPING_HINT = "模型输出的每个标签计入一个类别，一个类别可关联多个标签，任一标签命中即算命中；至少保留一个标签关联到类别。改动对之后开始的任务生效。";
const NONE = "_none";
/** Labels shown beside a category's chevron; the rest collapse into “+N”. */
const MAX_CHIPS = 3;

/**
 * The one menu for a single label, wherever it is shown (in a category's row or among the unused labels): the
 * category it counts as now is ticked, and “不使用” is the last choice. A label counts as one category, so a pick
 * moves it there at once. Every category of the catalog is offered, review-only ones included.
 */
function openLabelMenu(anchor: HTMLElement, m: ModelPlugin, label: string): void {
  const { categories: catalog } = getState();
  const current = effectiveLabels(m);
  const now = current[label];
  const ids = byCatalogOrder(catalog, [...catalog.map((c) => c.id), ...(now ? [now] : [])]);
  openMenu(anchor, [
    { h: label + " · 计入类别" },
    ...ids.map((id) => ({ l: categoryName(catalog, id), k: id, on: now === id })),
    "-",
    { l: "不使用", k: NONE, on: !now },
  ], (it) => {
    const to = String(it.k);
    if (to === NONE) { if (now) void setLabelCategory(m.id, label, null); return; }
    if (to === now) return;
    // through the category's whole label set: a label moved from another category is said so
    void setCategoryLabels(m.id, to, [...labelsOfCategory(current, to), label]);
  });
}

/** Models: the list on the left, the selected model's labels and the categories they count as on the right. */
export function ModelMappingEditor() {
  const models = useAppState((s) => s.models);
  const presets = useAppState((s) => s.presets);
  const catalog = useAppState((s) => s.categories);
  const selected = useAppState((s) => s.modelSel);
  const [creating, setCreating] = useState(false);
  const pane = useCategoryFocus("models");
  const m = models.find((x) => x.id === selected) ?? models[0];
  const list = (
    <ItemList
      label="模型"
      items={models.map((x) => ({ id: x.id, icon: "cpu", title: x.name, subtitle: [Object.keys(x.labels ?? {}).length + " 个标签", x.mapping ? "已自定义" : "默认映射"].join(" · ") }))}
      selectedId={m?.id}
      onSelect={selectModel}
    />
  );
  if (!m) {
    return (
      <div className="split">
        {list}
        <div className="preset-detail">
          <GuideCard icon="cpu" title="还没有模型" actions={<Button variant="primary" icon="puzzle" onClick={() => showModels()}>去插件页安装</Button>}>
            模型随插件安装；装好后在这里设置它的标签计入哪个类别。
          </GuideCard>
        </div>
      </div>
    );
  }
  const labels = m.labels ?? {};
  const declared = Object.keys(labels).length > 0;
  const current = effectiveLabels(m);
  const name = (category: string) => categoryName(catalog, category);
  // the same categories the scheme page lists for this model: review-only ones only once a label counts as one
  const rows = listedCategories(catalog, Object.values(current));
  const unused = unmappedLabels(m);
  const using = presets.filter((p) => p.det === "model" && p.model === m.id);
  const meta = [m.version && "v" + m.version, m.variant, m.languages].filter(Boolean).join(" · ");
  const pick = (anchor: HTMLElement, category: string) =>
    openMultiMenu(anchor, name(category) + " · 可多选标签", Object.keys(labels).sort().map((l) => ({
      k: l, l, s: current[l] ? name(current[l]!) : "未关联",
    })), labelsOfCategory(current, category), (chosen) => { void setCategoryLabels(m.id, category, chosen); }, { alignRight: true });

  return (
    <div className="split">
      {list}
      <div className="preset-detail" ref={pane} role="region" aria-label="模型类别映射" tabIndex={0}>
        <ConfigHeader
          name={m.name}
          badge={m.mapping ? "已自定义" : "默认映射"}
          badgePrimary={!!m.mapping}
          subtitle={<>
            {meta}
            {using.length > 0 && <>{meta && " · "}用于 <RefLinks items={using} onOpen={(id) => showInConfig({ tab: "scheme", id })} /></>}
          </>}
        >
          <Button variant="ghost" disabled={!m.mapping} onClick={() => void resetModelMapping(m.id)}>恢复默认</Button>
        </ConfigHeader>
        <CatalogNotice />
        <SectionTitle first>
          标签映射
          <span className="sec-help" tabIndex={0} aria-label={MAPPING_HINT} {...tip(MAPPING_HINT, { wrap: true })}><Icon name="info" size={13} /></span>
          <span className="sp" />
          <Button variant="ghost" icon="plus" disabled={creating} onClick={() => setCreating(true)}>新建类别</Button>
        </SectionTitle>
        {creating && <NewCategoryForm idPrefix="mm" onCreated={() => setCreating(false)} onCancel={() => setCreating(false)} />}
        <List className="mapping-list">
          {rows.map((category) => {
            const chosen = labelsOfCategory(current, category);
            const defaults = labelsOfCategory(labels, category);
            const modified = JSON.stringify(chosen) !== JSON.stringify(defaults);
            const hidden = chosen.slice(MAX_CHIPS);
            return (
              <CategoryRow
                key={category}
                category={category}
                className="mapping-row"
                tags={modified ? <Tag {...tip("默认值：" + (defaults.join("、") || "未设置"), { align: "start" })}>已修改</Tag> : undefined}
              >
                <span className="mapping-labels">
                  {chosen.length
                    ? chosen.slice(0, MAX_CHIPS).map((l) => <button key={l} type="button" className="tag mono label-chip" title={l} aria-label={"标签 " + l} onClick={(e) => openLabelMenu(e.currentTarget, m, l)}>{l}</button>)
                    : <span className="muted sm">未设置</span>}
                  {hidden.length > 0 && <button type="button" className="tag mono label-more" disabled={!declared} aria-label={"另有 " + hidden.length + " 个标签：" + hidden.join("、")} {...tip(hidden.join("、"), { align: "end", wrap: true })} onClick={(e) => pick(e.currentTarget, category)}>+{hidden.length}</button>}
                </span>
                <SelectButton className={declared ? undefined : "sel-off"} disabled={!declared} aria-label={name(category) + " 关联的标签"} onClick={(e) => pick(e.currentTarget, category)}>{null}</SelectButton>
                {catalog.find((c) => c.id === category)?.origin === "custom" && (
                  <IconButton icon="trash" size={15} label={"删除类别 " + name(category)} tooltip="删除类别（全局共用）" tooltipAlign="end" onClick={() => deleteCategory(category)} />
                )}
              </CategoryRow>
            );
          })}
        </List>
        <div className="unmapped-labels">
          <SectionTitle>未关联的标签{declared && <span className="sec-note">{unused.length} 个</span>}</SectionTitle>
          {!declared ? <span className="muted sm">这个模型没有声明标签</span>
            : unused.length ? unused.map((label) => <Button key={label} className="mono" onClick={(e) => openLabelMenu(e.currentTarget, m, label)}>{label}</Button>)
              : <span className="muted sm">没有未关联的标签</span>}
        </div>
      </div>
    </div>
  );
}
