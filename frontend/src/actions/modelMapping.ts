// A model's labels and the categories they count as. The default comes with the model (its manifest); the
// person may send a label to another category, a category of their own included, or not use it. The
// kernel stores the change as the model's "model_mapping" object; resetting deletes it.
import { effectiveLabels, mappedCategories, sameMapping, withCategoryLabels } from "../domain/liveConfig";
import { categoryName } from "../domain/categories";
import { availableRulesets, losesNothing, mappingChangeLoss } from "../domain/presets";
import { confirmLoss } from "./presets";
import type { ModelPlugin } from "../domain/types";
import { toast } from "../overlay/store";
import { getState, store } from "../store";
import { liveEngine } from "./live";
import { configErrorText, setModelCategories } from "./liveConfig";

/** Applies a mapping to the model on screen (and to what the scheme screens offer for it). */
function show(modelId: string, map: Record<string, string> | null, stored?: { id: string; version: number }): void {
  let categories: string[] = [];
  store.update((d) => {
    const m = d.models.find((x) => x.id === modelId);
    if (!m) return;
    if (map) m.mapping = { map, ...(stored ? { stored } : {}) };
    else delete m.mapping;
    m.categories = categories = mappedCategories(effectiveLabels(m));
    // a stored scheme handles only what it stores: a category the mapping adds is off there until set
    d.presets.forEach((p) => {
      if (p.stored && p.det === "model" && p.model === modelId) categories.forEach((c) => { p.cats[c] ??= "off"; });
    });
  });
  setModelCategories(modelId, categories);
}

/** Stores `map` as the model's mapping; the default itself is stored as no mapping at all. */
async function write(m: ModelPlugin, map: Record<string, string>): Promise<boolean> {
  const isDefault = sameMapping(map, m.labels ?? {});
  const e = liveEngine();
  if (!e) {
    show(m.id, isDefault ? null : map);
    return true;
  }
  const stored = m.mapping?.stored;
  try {
    if (isDefault) {
      if (stored) await e.configDelete(stored.id, stored.version);
      show(m.id, null);
      return true;
    }
    const content = JSON.stringify({ model_id: m.id, label_map: map });
    const it = stored ? await e.configSave(stored.id, stored.version, m.id, "", content) : await e.configCreate("model_mapping", m.id, "", content);
    show(m.id, map, { id: it.id, version: it.version });
    return true;
  } catch (err) {
    toast(configErrorText(err));
    return false;
  }
}

/** Queue the operation, including reading the current map and stored version, separately for each model. */
const queues = new Map<string, Promise<void>>();
function enqueue(modelId: string, operation: () => Promise<void>): Promise<void> {
  const next = (queues.get(modelId) ?? Promise.resolve()).then(operation, operation);
  queues.set(modelId, next);
  const release = () => { if (queues.get(modelId) === next) queues.delete(modelId); };
  void next.then(release, release);
  return next;
}

type MappingEdit = (m: ModelPlugin) => { map: Record<string, string>; message?: string };
function change(modelId: string, edit: MappingEdit, approved = false): Promise<void> {
  return enqueue(modelId, async () => {
    const s = getState();
    const m = s.models.find((x) => x.id === modelId);
    if (!m) return;
    const { map, message } = edit(m);
    if (sameMapping(map, effectiveLabels(m))) return;
    const loss = mappingChangeLoss(s.presets, { rulesets: availableRulesets(s.rulesets, s.plugins), models: s.models }, modelId, map);
    if (!approved && !losesNothing(loss)) {
      confirmLoss("保存映射", loss, () => { void change(modelId, edit, true); });
      return;
    }
    if (await write(m, map) && message) toast(message);
  });
}

/** Sends one label of a model to a category, or (null) stops using it. */
export function setLabelCategory(modelId: string, label: string, category: string | null): Promise<void> {
  return change(modelId, (m) => {
    const map = { ...effectiveLabels(m) };
    if (m.labels && label in m.labels) {
      if (category == null) delete map[label];
      else map[label] = category;
    }
    return { map };
  });
}

/** Exactly these labels report the category; labels already used elsewhere move here. One whole-map write. */
export function setCategoryLabels(modelId: string, category: string, labels: string[]): Promise<void> {
  return change(modelId, (m) => {
    const { map, moved } = withCategoryLabels(effectiveLabels(m), category, labels, m.labels ?? {});
    const catalog = getState().categories;
    const from = [...new Set(moved.map((x) => categoryName(catalog, x.from)))];
    return { map, message: from.length ? from.map((n) => "已从 " + n + " 移到 " + categoryName(catalog, category)).join("；") : undefined };
  });
}

/** Back to the mapping the model comes with. */
export function resetModelMapping(modelId: string): Promise<void> {
  return change(modelId, (m) => ({ map: { ...(m.labels ?? {}) }, message: "已恢复默认映射" }));
}
