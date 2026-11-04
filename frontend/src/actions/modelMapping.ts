// A model's labels and the categories they count as. The default comes with the model (its manifest); the
// person may send a label to another category, a category of their own included, or not use it. The
// kernel stores the change as the model's "model_mapping" object; resetting deletes it.
import { effectiveLabels, mappedCategories, sameMapping, withCategoryLabels } from "../domain/liveConfig";
import { categoryName } from "../domain/categories";
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
function change(modelId: string, edit: MappingEdit): Promise<void> {
  return enqueue(modelId, async () => {
    const m = getState().models.find((x) => x.id === modelId);
    if (!m) return;
    const { map, message } = edit(m);
    if (sameMapping(map, effectiveLabels(m))) return;
    // a model none of whose labels count detects nothing: a scheme that wants that detects with rules only
    if (!Object.keys(map).length) { toast("至少保留一个标签关联到类别"); return; }
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
    const messages = moved.slice(0, 3).map(({ label, from }) => "已将 " + label + " 从 " + categoryName(catalog, from) + " 移到 " + categoryName(catalog, category));
    if (moved.length > 3) messages.push("等 " + (moved.length - 3) + " 个标签");
    return { map, message: messages.join("；") || undefined };
  });
}

/** Back to the mapping the model comes with. */
export function resetModelMapping(modelId: string): Promise<void> {
  return change(modelId, (m) => ({ map: { ...(m.labels ?? {}) }, message: "已恢复默认映射" }));
}
