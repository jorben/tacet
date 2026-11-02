// User categories: a person names a new category (e.g. while writing a rule) and rules, schemes and review
// use it like any other. Stored by the kernel as config objects of kind "category"; in the browser demo
// they live in the store.
import { applyCategoryColors, type CategoryInfo } from "../domain/categories";
import type { CategoryDef } from "../domain/liveConfig";
import { getState, store } from "../store";
import { liveEngine } from "./live";
import { loadCategories } from "./liveConfig";
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
