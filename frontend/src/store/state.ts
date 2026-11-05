import { STANDARD_CATEGORIES, type CategoryInfo } from "../domain/categories";
import { DEFAULT_PRESET_NAME, seedModels, seedPlugins, seedPresets, seedRulesets, seedSettings } from "../domain/seed";
import type {
  Batch, DecisionSnapshot, HistoryEntry, HistoryResult, ModelPending, ModelPlugin, NavEntry, PluginInfo,
  Preset, PresetTab, Ruleset, Settings, Single, ThemePref, UpdateState, View, ViewMode,
} from "../domain/types";

export interface AppState {
  view: View;
  /** Where “返回” leads when a page was opened from another page. */
  navStack: NavEntry[];

  models: ModelPlugin[];
  /** The category catalog: names, placeholders and colors of the ids rules, schemes and findings carry. */
  categories: CategoryInfo[];
  /** Demo categories are ready immediately; live requests temporarily suspend registration labels. */
  categoryStatus: "loading" | "ready" | "failed";
  modelPending: ModelPending | null;

  theme: "light" | "dark";
  themePref: ThemePref;
  settings: Settings;
  update: UpdateState;

  presets: Preset[];
  rulesets: Ruleset[];
  plugins: PluginInfo[];
  homePreset: string;
  presetSel: string;
  presetTab: PresetTab;
  rulesetSel: string | null;
  /** The model the models tab shows (null: the first); kept across tabs so a link can point at one. */
  modelSel: string | null;
  /** A fresh configuration load resets transient editor state, even if the selected id stays the same. */
  configLoad: number;

  /** The single-file task and the batch on screen; others are under “最近”, and go on there if they have work (see `makeRoom`). */
  single: Single | null;
  batch: Batch | null;
  /** The single task set aside while a file of a batch is open for review. */
  behind: { single: Single; undo: DecisionSnapshot[] } | null;
  /** The file of a batch being read after a click: it replaces the screen when it arrives, unless another click came first. */
  fileOpening: { batch: string; name: string } | null;
  /** Undo stack of the open single document. */
  undo: DecisionSnapshot[];
  /** View mode shared between the single review and the batch review. */
  docMode: ViewMode;

  history: HistoryEntry[];
  /** `data` is null while looking at the result of the live batch. */
  historyResult: HistoryResult | null;
}

const prefersDark = (): boolean => typeof window !== "undefined" && !!window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;

export function createInitialState(): AppState {
  return {
    view: "home",
    navStack: [],
    models: seedModels(),
    categories: STANDARD_CATEGORIES.map((c) => ({ ...c })),
    categoryStatus: "ready",
    modelPending: null,
    theme: prefersDark() ? "dark" : "light",
    themePref: "system",
    settings: seedSettings(),
    update: { current: "", latest: null, status: "idle" },
    presets: seedPresets(),
    rulesets: seedRulesets(),
    plugins: seedPlugins(),
    homePreset: DEFAULT_PRESET_NAME,
    presetSel: "office",
    presetTab: "scheme",
    rulesetSel: null,
    modelSel: null,
    configLoad: 0,
    single: null,
    batch: null,
    behind: null,
    fileOpening: null,
    undo: [],
    docMode: "orig",
    history: [],
    historyResult: null,
  };
}
