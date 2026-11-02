// A model's labels and the categories they count as. The default comes with the model (its manifest); the
// person may send a label to another category, a category of their own included, or not use it. The
// kernel stores the change as the model's "model_mapping" object; resetting deletes it.
import { effectiveLabels, mappedCategories, sameMapping } from "../domain/liveConfig";
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

/** Sends one label of a model to a category, or (null) stops using it. */
export async function setLabelCategory(modelId: string, label: string, category: string | null): Promise<void> {
  const m = getState().models.find((x) => x.id === modelId);
  if (!m?.labels || !(label in m.labels)) return;
  const map = { ...effectiveLabels(m) };
  if (category == null) delete map[label];
  else map[label] = category;
  if (sameMapping(map, effectiveLabels(m))) return;
  await write(m, map);
}

/** Back to the mapping the model comes with. */
export async function resetModelMapping(modelId: string): Promise<void> {
  const m = getState().models.find((x) => x.id === modelId);
  if (!m?.mapping) return;
  if (await write(m, { ...(m.labels ?? {}) })) toast("已恢复默认映射");
}
