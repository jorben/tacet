// The category catalog. Categories are data the engine lists (standard vocabulary, categories a model's
// manifest declares, categories the user creates); rules, schemes and findings carry only the id. Pure:
// every lookup takes the catalog, the store holds it.

/** A category id as the engine uses it (e.g. "phone", "id_number", or a user category's "cfg_…"). */
export type CategoryId = string;

export type CategoryOrigin = "standard" | "model" | "custom";

export interface CategoryInfo {
  id: CategoryId;
  name: string;
  /** The text written in place of a value when the category is handled with a placeholder. */
  placeholder: string;
  color?: string;
  /** Offered when a selection is marked by hand, whether or not the scheme detects it. */
  manual?: boolean;
  /**
   * Marked by hand in review (company names, other): a scheme lists it only when one of its models or rules
   * reports it. Every other category is listed by every scheme.
   */
  reviewOnly?: boolean;
  origin: CategoryOrigin;
  /** Version of a user category's stored object (absent for shipped and model-declared ones). */
  version?: number;
}

/**
 * The shipped standard vocabulary (internal/categories/standard-v1.json): the catalog of the demo and
 * the catalog until the engine has answered. The engine's list replaces it.
 */
export const STANDARD_CATEGORIES: CategoryInfo[] = [
  { id: "person", name: "人名", placeholder: "[姓名]", color: "#6550C8", manual: true, origin: "standard" },
  { id: "phone", name: "电话", placeholder: "[手机号]", color: "#0B7A8F", manual: true, origin: "standard" },
  { id: "email", name: "邮箱", placeholder: "[邮箱]", color: "#2F6FB5", origin: "standard" },
  { id: "id_number", name: "身份证", placeholder: "[证件号]", color: "#B4570B", manual: true, origin: "standard" },
  { id: "address", name: "地址", placeholder: "[地址]", color: "#4E7A12", manual: true, origin: "standard" },
  { id: "payment_card", name: "银行卡", placeholder: "[银行卡]", color: "#B0234F", origin: "standard" },
  { id: "date", name: "日期", placeholder: "[日期]", color: "#8A4FC2", origin: "standard" },
  { id: "account", name: "账号", placeholder: "[账号]", color: "#9C3D7A", origin: "standard" },
  { id: "ip_address", name: "IP 地址", placeholder: "[IP]", color: "#2D7A5C", origin: "standard" },
  { id: "url", name: "网址", placeholder: "[链接]", color: "#3D5A99", origin: "standard" },
  { id: "secret", name: "凭证", placeholder: "[凭证]", color: "#A03E1F", origin: "standard" },
  { id: "organization", name: "公司名", placeholder: "[机构]", color: "#8C6A1F", manual: true, reviewOnly: true, origin: "standard" },
  { id: "other", name: "其他", placeholder: "[已处理]", color: "#5B6470", manual: true, reviewOnly: true, origin: "standard" },
];

/** The id hand marks fall back to, and the category of anything a person cannot otherwise name. */
export const OTHER: CategoryId = "other";

/** Colors given to categories that bring none (user categories), picked by id so they stay stable. */
const PALETTE = ["#0F766E", "#7C3AED", "#B45309", "#BE185D", "#1D4ED8", "#4D7C0F", "#9F1239", "#0E7490", "#6D28D9", "#A16207"];

function hash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h;
}

export const colorOf = (c: Pick<CategoryInfo, "id" | "color">): string => c.color || PALETTE[hash(c.id) % PALETTE.length]!;

/** The category with this id; an id the catalog does not know is shown as itself. */
export function categoryOf(list: readonly CategoryInfo[], id: CategoryId): CategoryInfo {
  return list.find((c) => c.id === id) ?? { id, name: id, placeholder: "[已处理]", origin: "custom" };
}

export const categoryName = (list: readonly CategoryInfo[], id: CategoryId): string => categoryOf(list, id).name;
export const categoryPlaceholder = (list: readonly CategoryInfo[], id: CategoryId): string => categoryOf(list, id).placeholder;

/** Ids sorted by catalog order; ids the catalog does not know come last, by id. */
export function byCatalogOrder(list: readonly CategoryInfo[], ids: Iterable<CategoryId>): CategoryId[] {
  const rank = (id: string) => { const i = list.findIndex((c) => c.id === id); return i < 0 ? list.length : i; };
  return [...new Set(ids)].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
}

/** Categories a selection can be marked as by hand: those flagged so, and every user category. */
export const manualCategories = (list: readonly CategoryInfo[]): CategoryInfo[] => list.filter((c) => c.manual || c.origin === "custom");

/**
 * Category colors are CSS custom properties `--t-<id>`, so that swatches and highlights of any category,
 * including ones created later, read the same variable. Called whenever the catalog changes.
 */
export function applyCategoryColors(list: readonly CategoryInfo[], root: HTMLElement | null = typeof document === "undefined" ? null : document.documentElement): void {
  if (!root) return;
  for (const c of list) root.style.setProperty("--t-" + c.id, colorOf(c));
}
