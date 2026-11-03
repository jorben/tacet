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
  /** A fresh configuration load resets transient editor state, even if the selected id stays the same. */
  configLoad: number;

  /** One single-file task and one batch run at a time (see `needSlot`). */
  single: Single | null;
  batch: Batch | null;
  /** The single task parked while a file of a batch is open for review. */
  behind: { single: Single; undo: DecisionSnapshot[] } | null;
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
    configLoad: 0,
    single: null,
    batch: null,
    behind: null,
    undo: [],
    docMode: "orig",
    history: [],
    historyResult: null,
  };
}
