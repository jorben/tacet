import { useState } from "react";
import { resetModelMapping, setLabelCategory } from "../../actions/modelMapping";
import { byCatalogOrder, categoryName } from "../../domain/categories";
import { effectiveLabels } from "../../domain/liveConfig";
import { presetTreatment } from "../../domain/presets";
import type { Preset } from "../../domain/types";
import { openMenu } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Empty, List, SelectButton, Swatch, Tag } from "../../ui";
import { ItemList } from "./ItemList";

const NOT_USED = "\u0000none";

/** The categories a model now produces that a scheme using it does not handle: what it finds there is dropped. */
export function unhandledCategories(p: Preset, categories: string[]): string[] {
  return categories.filter((c) => !["mask", "ph"].includes(presetTreatment(p, c)));
}

/** Models: the list on the left, the selected model's labels and the categories they count as on the right. */
export function ModelMappingEditor() {
  const models = useAppState((s) => s.models);
  const presets = useAppState((s) => s.presets);
  const catalog = useAppState((s) => s.categories);
  const [selected, setSelected] = useState<string | null>(null);
  const m = models.find((x) => x.id === selected) ?? models[0];
  if (!m) return <Empty padding="36px 0">还没有模型</Empty>;
  const labels = m.labels ?? {};
  const current = effectiveLabels(m);
  // by what the label means by default, in catalog order, then by label
  const order = byCatalogOrder(catalog, Object.values(labels));
  const rows = Object.keys(labels).sort((a, b) => order.indexOf(labels[a]!) - order.indexOf(labels[b]!) || a.localeCompare(b));
  const using = presets.filter((p) => p.det === "model" && p.model === m.id);
  const gaps = using.map((p) => [p, unhandledCategories(p, m.categories ?? [])] as const).filter(([, c]) => c.length);
  const pick = (anchor: HTMLElement, label: string) =>
    openMenu(anchor, [
      { h: label },
      ...catalog.map((c) => ({ l: c.name, k: c.id, on: current[label] === c.id, s: c.id === labels[label] ? "默认" : undefined })),
      "-",
      { l: "不使用", k: NOT_USED, on: !(label in current), s: "命中丢弃" },
    ], (it) => void setLabelCategory(m.id, label, it.k === NOT_USED ? null : String(it.k)), { alignRight: true });

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
          模型输出的每个标签计入一个应用类别；方案按类别设置处理方式。改动对之后开始的任务生效。
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
          {rows.length ? rows.map((label) => {
            const to = current[label];
            return (
              <div className="row mapping-row" key={label}>
                <Swatch category={to ?? "other"} />
                <span className="rl">
                  <span className="mono">{label}</span>
                  {to !== labels[label] && <span className="muted sm">默认：{categoryName(catalog, labels[label]!)}</span>}
                </span>
                <span className="rv">
                  <SelectButton aria-label={label + " 计入的类别"} onClick={(e) => pick(e.currentTarget, label)}>
                    {to ? categoryName(catalog, to) : <span className="muted">不使用</span>}
                  </SelectButton>
                </span>
              </div>
            );
          }) : <Empty padding="36px 0">这个模型没有声明标签</Empty>}
        </List>
      </div>
    </div>
  );
}
