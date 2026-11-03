import { useState } from "react";
import { deleteCategory } from "../../actions/categories";
import { resetModelMapping, setCategoryLabels, setLabelCategory } from "../../actions/modelMapping";
import { NewCategoryForm } from "../dialogs/NewCategoryForm";
import { byCatalogOrder, categoryName } from "../../domain/categories";
import { effectiveLabels, labelsOfCategory, unmappedLabels } from "../../domain/liveConfig";
import { presetTreatment } from "../../domain/presets";
import type { Preset } from "../../domain/types";
import { openMenu, openMultiMenu } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Empty, IconButton, List, SelectButton, Swatch, Tag, tip } from "../../ui";
import { ItemList } from "./ItemList";


/** The categories a model now produces that a scheme using it does not handle: what it finds there is dropped. */
export function unhandledCategories(p: Preset, categories: string[]): string[] {
  return categories.filter((c) => !["mask", "ph"].includes(presetTreatment(p, c)));
}

/** Models: the list on the left, the selected model's labels and the categories they count as on the right. */
export function ModelMappingEditor() {
  const models = useAppState((s) => s.models);
  const presets = useAppState((s) => s.presets);
  const catalog = useAppState((s) => s.categories);
  const loading = useAppState((s) => s.categoryStatus) === "loading";
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const m = models.find((x) => x.id === selected) ?? models[0];
  if (!m) return <Empty padding="36px 0">还没有模型</Empty>;
  const labels = m.labels ?? {};
  const current = effectiveLabels(m);
  const entityName = (category: string) => loading ? category : categoryName(catalog, category);
  const rows = byCatalogOrder(catalog, [...catalog.map((c) => c.id), ...Object.values(current)]);
  const unused = unmappedLabels(m);
  const using = presets.filter((p) => p.det === "model" && p.model === m.id);
  const gaps = using.map((p) => [p, unhandledCategories(p, m.categories ?? [])] as const).filter(([, c]) => c.length);
  const pick = (anchor: HTMLElement, category: string) =>
    openMultiMenu(anchor, entityName(category) + " · 可多选标签", Object.keys(labels).sort().map((l) => ({
      k: l, l, s: current[l] ? categoryName(catalog, current[l]!) : "未关联",
    })), labelsOfCategory(current, category), (chosen) => { void setCategoryLabels(m.id, category, chosen); }, { alignRight: true });

  return (
    <div className="split">
      <ItemList
        items={models.map((x) => ({ id: x.id, icon: "cpu", title: x.name, subtitle: [x.variant, Object.keys(x.labels ?? {}).length + " 个标签", x.mapping ? "已自定义" : "默认映射"].filter(Boolean).join(" · ") }))}
        selectedId={m.id}
        onSelect={setSelected}
      />
      <div className="preset-detail" role="region" aria-label="模型类别映射" tabIndex={0}>
        <div className="phead">
          <div style={{ minWidth: 0 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
              <div className="h1">{m.name}</div>
              <Tag primary={!!m.mapping}>{m.mapping ? "已自定义" : "默认映射"}</Tag>
            </div>
            <div className="muted sm" style={{ marginTop: 4 }}>
              {[m.version && "v" + m.version, m.variant, m.languages].filter(Boolean).join(" · ")}
              {" · "}
              {using.length ? "用于 " + using.map((p) => p.name).join("、") : "未被方案使用"}
            </div>
          </div>
          <span className="sp" />
          <Button variant="ghost" disabled={!m.mapping} onClick={() => void resetModelMapping(m.id)}>
            恢复默认
          </Button>
        </div>
        <p className="muted sm mapping-hint">
          模型输出的每个标签计入一个类别，一个类别可关联多个标签，任一标签命中即算命中；至少保留一个标签关联到类别。方案按类别设置处理方式，改动对之后开始的任务生效。
        </p>
        {gaps.length > 0 && (
          <div className="mapping-warn" role="note">
            {gaps.map(([p, cats]) => (
              <div key={p.id}>
                「{p.name}」没有为 {cats.map((c) => categoryName(catalog, c)).join("、")} 设置处理方式，模型的这些命中不会被处理
                {p.builtin ? "（内建方案只读，复制后可设置）" : "，请在方案中设置"}。
              </div>
            ))}
          </div>
        )}
        <List>
          {rows.map((category) => {
            const chosen = labelsOfCategory(current, category);
            const defaults = labelsOfCategory(labels, category);
            const modified = JSON.stringify(chosen) !== JSON.stringify(defaults);
            return (
              <div className="row mapping-row" data-category={category} key={category}>
                <Swatch category={category} />
                <div className="mapping-main">
                  <div className="rl"><span className="entity-name">{entityName(category)}</span>
                    {!loading && !catalog.some((c) => c.id === category) && <Tag>未登记类别</Tag>}
                    {modified && <Tag {...tip("默认值：" + (defaults.join("、") || "未设置"))}>已修改</Tag>}
                  </div>
                  <div className="mapping-labels">{chosen.length ? chosen.map((l) => <span key={l} className="tag mono">{l}</span>) : <span className="muted sm">未设置</span>}</div>
                </div>
                <span className="rv">
                  <SelectButton disabled={!Object.keys(labels).length} aria-label={entityName(category) + " 关联的标签"} onClick={(e) => pick(e.currentTarget, category)}>选择标签</SelectButton>
                  {catalog.find((c) => c.id === category)?.origin === "custom" && (
                    <IconButton icon="trash" size={15} label={"删除类别 " + entityName(category)} tooltip="删除类别（全局共用）" tooltipAlign="end" onClick={() => deleteCategory(category)} />
                  )}
                </span>
              </div>
            );
          })}
        </List>
        {creating ? <NewCategoryForm idPrefix="mm" onCreated={() => setCreating(false)} onCancel={() => setCreating(false)} />
          : <Button variant="ghost" icon="plus" style={{ marginTop: 8 }} onClick={() => setCreating(true)}>新建类别</Button>}
        <div className="unmapped-labels">
          <div className="sec-t">未关联的标签</div>
          {unused.length ? unused.map((label) => <Button key={label} className="mono" onClick={(e) => openMenu(e.currentTarget, catalog.map((c) => ({ l: c.name, k: c.id })), (it) => { void setLabelCategory(m.id, label, String(it.k)); })}>{label}</Button>) : <span className="muted sm">没有未关联的标签</span>}
          {!Object.keys(labels).length && <p className="muted sm">这个模型没有声明标签</p>}
        </div>
      </div>
    </div>
  );
}
