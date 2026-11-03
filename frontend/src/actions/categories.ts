// User categories: a person names a new category (e.g. while writing a rule) and rules, schemes and review
// use it like any other. Stored by the kernel as config objects of kind "category"; in the browser demo
// they live in the store.
import { applyCategoryColors, type CategoryInfo } from "../domain/categories";
import { categoryUse, effectiveLabels, mappedCategories, type CategoryDef } from "../domain/liveConfig";
import { closeModal, openModal, toast, type ModalSpec } from "../overlay/store";
import { getState, store } from "../store";
import { liveEngine } from "./live";
import { configErrorText, flushConfig, loadCategories, loadConfig } from "./liveConfig";
import { nextId } from "./timers";

export type CreateCategoryResult = { id: string } | { error: "name" | "placeholder" | "taken" | "failed" };

const MAX_NAME = 32;
const MAX_PLACEHOLDER = 64;

/** The placeholder a new category gets unless the person writes one. */
export const defaultPlaceholder = (name: string): string => "[" + name.trim() + "]";

/** Creates a category. Names are unique across the whole catalog, shipped categories included. */
export async function createCategory(name: string, placeholder = defaultPlaceholder(name)): Promise<CreateCategoryResult> {
  const n = name.trim();
  const ph = placeholder.trim();
  if (!n || [...n].length > MAX_NAME) return { error: "name" };
  if (!ph || [...ph].length > MAX_PLACEHOLDER || /[\r\n]/.test(ph)) return { error: "placeholder" };
  if (getState().categories.some((c) => c.name === n)) return { error: "taken" };
  const e = liveEngine();
  if (!e) {
    const c: CategoryInfo = { id: nextId("cat"), name: n, placeholder: ph, manual: true, origin: "custom" };
    store.update((d) => { d.categories.push(c); });
    applyCategoryColors([c]);
    return { id: c.id };
  }
  try {
    const it = await e.configCreate("category", n, "", JSON.stringify({ placeholder: ph }));
    await loadCategories();
    return { id: it.id };
  } catch (err) {
    return { error: /CONFIG_NAME_CONFLICT/.test(String((err as Error)?.message ?? err)) ? "taken" : "failed" };
  }
}

/**
 * Asks to delete a user category. Categories are shared by every rule set, model and scheme: a rule that reports
 * it keeps it (the person is told which rule set), and so does a model that would be left with no label;
 * otherwise a dialog says what lets go of it and waits for a yes. `back` is the dialog to return to.
 */
export function deleteCategory(id: string, back?: ModalSpec): void {
  const s = getState();
  const c = s.categories.find((x) => x.id === id);
  if (!c || c.origin !== "custom") return;
  const use = categoryUse(id, s.rulesets, s.models, s.presets);
  if (use.rulesets.length) {
    toast("「" + c.name + "」正被规则组「" + use.rulesets[0] + "」的规则使用，不能删除");
    return;
  }
  const last = use.models.find((m) => m.last);
  if (last) {
    toast("模型「" + last.name + "」只有关联到「" + c.name + "」的标签，不能删除");
    return;
  }
  openModal({ type: "deleteCategory", id, back });
}

/** Deletes a user category the person confirmed; what refers to it lets go of it. Returns whether it is gone. */
export async function removeCategory(id: string): Promise<boolean> {
  const e = liveEngine();
  if (!e) {
    store.update((d) => {
      d.categories = d.categories.filter((c) => c.id !== id);
      d.presets.forEach((p) => { delete p.cats[id]; });
      d.models.forEach((m) => {
        if (!m.mapping) return;
        for (const [label, c] of Object.entries(m.mapping.map)) if (c === id) delete m.mapping.map[label];
        m.categories = mappedCategories(effectiveLabels(m));
      });
    });
    return true;
  }
  try {
    await flushConfig(); // the schemes' pending edits first: the kernel changes the stored ones
    const c = getState().categories.find((x) => x.id === id);
    if (!c) return true;
    await e.configDelete(id, c.version ?? 1);
    return true;
  } catch (err) {
    toast(configErrorText(err));
    return false;
  } finally {
    await loadConfig().catch(() => {});
    await loadCategories();
  }
}

/** The dialog's yes: delete, then return to where the person was. */
export async function confirmDeleteCategory(id: string, back?: ModalSpec): Promise<void> {
  const name = getState().categories.find((c) => c.id === id)?.name ?? id;
  const gone = await removeCategory(id);
  if (back) openModal(back); else closeModal();
  if (gone) toast("已删除类别「" + name + "」");
}

/**
 * The ids of this workspace for the user categories an exchange file brings: a category of the same name
 * is the same category; any other is created. Returns exporting id → local id.
 */
export async function adoptCategories(defs: CategoryDef[]): Promise<Map<string, string>> {
  const map = new Map<string, string>();
  for (const d of defs) {
    const same = getState().categories.find((c) => c.name === d.name.trim());
    if (same) { map.set(d.id, same.id); continue; }
    const made = await createCategory(d.name, d.placeholder);
    if ("id" in made) map.set(d.id, made.id);
  }
  return map;
}
