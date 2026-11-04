// Domain types shared by the store, the pure domain logic and the UI. Nothing here depends on React.

/* ── Categories, documents and single-file review ─────────────────────────── */

import type { CategoryId } from "./categories";
export type { CategoryId } from "./categories";
/** How a category is written to the output: block it out, replace by a placeholder, or leave it alone. */
export type Treatment = "mask" | "ph";
/** Preset-level handling: a treatment, switched off, or outside the scope of rules-only detection. */
export type PresetTreatment = Treatment | "off" | "na";
export type ImageAction = "remove" | "keep";
export type ViewMode = "orig" | "fx" | "cmp";

export interface DocLocation {
  page: number;
  para: number;
  sec: string;
  /** Live DOCX: the approximate page the saved page markers give (0: none, no page is shown). */
  hint?: number;
  /** In a table: “表 k 第 r 行第 c 列”. */
  cell?: string;
}

export interface Hit extends DocLocation {
  id: number;
  type: CategoryId;
  text: string;
  /** Low-confidence hit: masked by default, listed under “待看” until the user has looked at it. */
  low: boolean;
  seen: boolean;
  src: string;
  state: "redact" | "keep";
  manual?: boolean;
}

export interface DocImage extends DocLocation {
  kind: "photo" | "seal";
  label: string;
  act: ImageAction;
}

export type Segment = { t: string; h?: undefined } | { h: number; t?: undefined };
export type BlockKind = "title" | "meta" | "h" | "pi" | "p" | "sign" | "img";

export interface Block {
  k: BlockKind;
  segs?: Segment[];
  at: DocLocation;
  /** Index into `imgs` for `img` and `sign` blocks. */
  img?: number;
}

export interface DocContent {
  hits: Hit[];
  imgs: DocImage[];
  pages: Block[][];
}

/** The part of a single document that user decisions change; the undo stack stores these. */
export interface DecisionSnapshot extends DocContent {
  catAct: CategoryTreatments;
  /** Live documents: undoing this entry takes back the manual redaction with this engine finding id. */
  manual?: number;
}
export type CategoryTreatments = Record<CategoryId, Treatment>;

export type SinglePhase = "processing" | "review" | "exporting" | "done";

export interface Single extends DocContent {
  name: string;
  ext: string;
  size: string;
  phase: SinglePhase;
  /** Progress of the two-step stepper (read → detect, export → verify). */
  stage: number;
  sp: number;
  mode: ViewMode;
  /** Id of the focused hit, -1 for none. */
  focus: number;
  catAct: CategoryTreatments;
  preset: string;
  modelId: string | null;
  out: string;
  sameAll: boolean;
  from: null | "batch";
  /** Page nearest the top of the scroll area. */
  page: number;
  /** Position of the file in its batch when opened from one. */
  bfi?: number;
  /** The fx copy of a hit (compare view) owns the open popover. */
  popFx?: boolean;
  /** Ask the document to scroll to a hit and open its popover; `seq` makes repeated requests distinct. */
  reveal?: { id: number; seq: number };
  resultPhase?: "result" | "manifest";
  manifestFilter?: ManifestFilter;
  manifestLimit?: number;
  /** Identifies the current processing/export run so a stale timer can tell it was superseded. */
  runId: number;
  /** Set when the task runs on the real engine: which batch and file it is, and how the document maps back. */
  live?: LiveSingle;
}

/** One rendered page: the image the engine serves, and where the candidates lie on it (pixels). */
export interface RasterPage {
  image: string;
  width: number;
  height: number;
  boxes: { finding: number; rect: [number, number, number, number] }[];
  unmapped: number;
}
export interface RasterDoc {
  pages: Record<number, RasterPage>;
  failed: Record<number, boolean>;
}

/** Link between a displayed document and the engine's file. */
export interface LiveSingle {
  batch: string;
  job: string;
  /** Engine finding id of each hit, by hit id. */
  findingOf: number[];
  /** Engine unit sequence number of each block, by page and block. */
  seqOf: number[][];
  /** Only the first part of a very long document is shown. */
  truncated: boolean;
  /** A PDF is shown as its page images with the candidates outlined; `pages` counts them. */
  raster?: RasterDoc;
  /** Name and checksum of the published file once done. */
  output?: string;
  digest?: string;
  /** The file waits for its password: the kernel lets the next task past it, even though nothing was read yet. */
  locked?: boolean;
  /** Choices went to the kernel when the task left the screen; going back on them has to reach the kernel too. */
  sent?: boolean;
}

/* ── Models, presets, rule sets, plugins ──────────────────────────────────── */

export type ModelStatus = "missing" | "downloading" | "paused" | "verifying" | "loading" | "ready" | "error";
export type ModelErrorKind = "offline" | "disk" | "integrity" | "load";

/** The user's mapping of a model's labels to categories (stored as a "model_mapping" object). */
export interface ModelMapping {
  /** model label → category id; a label left out is not used. */
  map: Record<string, string>;
  /** Stored object, once written. */
  stored?: { id: string; version: number };
}

export interface ModelPlugin {
  id: string;
  /** Categories this model's labels produce now: from the user's mapping, else from the default. */
  categories?: string[];
  /** The default mapping of every label the model emits (from its manifest): label → category id. */
  labels?: Record<string, string>;
  /** The user's mapping, when the default was changed. */
  mapping?: ModelMapping;
  name: string;
  version: string;
  variant: string;
  languages: string;
  weightMiB: number;
  recommended: boolean;
  /** Calibrated default and floor of the minimum-confidence threshold. Scores are not comparable across models. */
  confRec: number;
  confMin: number;
  status: ModelStatus;
  progress: number;
  error: ModelErrorKind | null;
  /** The machine has the memory the model needs (false: it cannot be downloaded here). */
  eligible?: boolean;
  /** Installed memory the model needs, in GiB; 0 or absent = any. */
  minMemoryGiB?: number;
  /** Where the files are and what the model computes on, as the kernel reports it; absent when not known. */
  runtime?: ModelRuntime;
}

/** Where the model files are, what they run on, and whether GPU acceleration is offered / on. */
export interface ModelRuntime {
  dir: string;
  backend: string;
  device: "cpu" | "gpu" | "auto";
  /** device is auto and the engine is confirming it right now (start-up probe). */
  detecting?: boolean;
  /** device is auto and the start-up probe could not confirm it: why (a reason code). */
  deviceCode?: string;
  /** Hugging Face id ("owner/name"); empty when unknown. */
  repository: string;
  accelerable: boolean;
  accelerated: boolean;
  /** The GPU build: its own download state (absent when the installation offers none). */
  accel?: { id: string; variant: string; status: ModelStatus; progress: number; error: ModelErrorKind | null; weightMiB: number };
}

/** An operation that waits for a model to become ready. The continuation itself lives in `store/pending.ts`. */
export interface ModelPending {
  label: string;
  preset: string;
  modelId: string | undefined;
}

export type Detection = "model" | "rules";
export type RuleKind = "词表" | "正则" | "校验";
export type Boundary = "" | "digit" | "word";

/** A rule as the rule editor holds it until it is saved. */
export interface RuleDraft {
  name: string;
  kind: RuleKind;
  cat: CategoryId;
  bd: Boundary;
  body: string;
}

export interface Rule {
  name: string;
  kind: RuleKind;
  /** The category a hit of this rule is reported as (an engine category id). */
  cat: CategoryId;
  body: string;
  bd?: Boundary;
  /** Number of word-list entries when the list itself is not kept in `body`. */
  n?: number;
  on: boolean;
  /** Id of the stored rule; a rule made on screen gets one when it is first stored. */
  rid?: string;
  /** Settings of the stored rule the screen has no control for (validator, context words, …). */
  ext?: Record<string, unknown>;
}

/** Where a displayed object is stored by the kernel, and which version of it was last seen there. */
export interface StoredRef {
  id: string;
  version: number;
  /** A scheme's own stored object (a preset is stored as preset + scheme). */
  schemeId?: string;
  schemeVersion?: number;
}

export interface VersionRecord<T> {
  ver: string;
  when: string;
  data: T;
}

export interface Preset {
  id: string;
  name: string;
  ver: string;
  builtin?: boolean;
  det: Detection;
  model: string;
  conf: number | null;
  rules: string[];
  cats: Record<string, PresetTreatment>;
  img: ImageAction;
  edited: string;
  hist?: VersionRecord<PresetContent>[];
  stored?: StoredRef;
}
export type PresetContent = Pick<Preset, "det" | "model" | "conf" | "rules" | "cats" | "img">;

export interface Ruleset {
  id: string;
  name: string;
  ver: string;
  builtin?: boolean;
  /** Id of the plugin that ships the rule set; the rule set is read-only and only listed while the plugin is on. */
  plugin?: string | null;
  edited?: string;
  rules: Rule[];
  hist?: VersionRecord<{ rules: Rule[] }>[];
  stored?: StoredRef;
}

export type PluginRole = "格式" | "规则" | "模型";
export interface PluginInfo {
  id: string;
  name: string;
  role: PluginRole;
  fmt: string;
  ver: string;
  builtin?: boolean;
  src?: string;
  commit?: string;
  on: boolean;
  trusted?: boolean;
  /** Recoverable tasks that still use this version. */
  refs?: number;
}

export type PresetTab = "scheme" | "rules" | "models";

/* ── Batches ──────────────────────────────────────────────────────────────── */

export type FileStatus = "queue" | "proc" | "wait" | "held" | "ready" | "done" | "partial" | "bad" | "skipped";
export type BatchPhase = "precheck" | "run" | "review" | "result" | "manifest";
export type BatchMode = "guided" | "auto" | "strict";
export type BatchTab = "todo" | "auto" | "files";
export type FileFilter = "all" | "done" | "run" | "wait" | "bad";
export type ManifestFilter = "all" | "ok" | "no";

export interface BatchFile {
  i: number;
  id: string;
  name: string;
  ext: string;
  pages: number;
  /** Hits per office category, as found by detection. */
  h: Record<CategoryId, number>;
  imgs: number;
  st: FileStatus;
  /** Ticks left of the current processing step. */
  t: number;
  /** Exception groups the file still waits in. */
  g: string[];
  /** Groups whose findings only appear after output validation. */
  late: string[];
  /** Detection has finished. */
  det: boolean;
  partial: boolean;
  /** At least one decision of this file was made by a person. */
  man: boolean;
  reason?: string | null;
  tmp?: boolean;
  /** Ticks left before a validated file is written out automatically. */
  rt?: number | null;
  /** Validation outcomes planned for the demo: re-runs, residue to widen, broken output. */
  vr?: number;
  vn?: number;
  res?: boolean;
  resN?: number;
  brk?: boolean;
  redo?: boolean;
  failWhy?: string;
  /** Output version, counted up when an exported file is exported again. */
  ver?: number;
  sz?: number;
  outputName?: string;
  /** Saved single-file review state of the file. */
  sv?: DecisionSnapshot | null;
  /** Checksum of the delivered file (real batches only). */
  sha?: string;
}

export interface GroupAction {
  l: string;
  k: string;
  n?: string;
}

export interface ReviewRecord {
  k: string;
  l: string;
  n: number;
  files?: number;
  mem?: number[];
  /** Made in the review screen (counts items, not files). */
  rv?: boolean;
  /** Identifies the record when an undo has to remove exactly this one. */
  uid?: number;
}

export interface ExceptionGroup {
  id: string;
  ic: string;
  t: string;
  unit: string;
  block?: boolean;
  gap?: boolean;
  noBulk?: boolean;
  post?: number;
  acts: GroupAction[];
  /** Items per member file. */
  per: Record<number, number>;
  done: ReviewRecord[];
  /** Files whose item was only found by output validation. */
  found?: Record<number, boolean>;
}

export interface ReviewItem {
  key: string;
  gid: string;
  gi: number;
  fi: number;
  j: number;
  p: number;
  dec: string | null;
}

export interface ReviewSnapshot {
  cur: string | null;
  decs: [string, string | null][];
  files: { fi: number; st: FileStatus; g: string[]; partial: boolean; t: number; man: boolean }[];
  done: ReviewRecord[][];
}

export interface ReviewState {
  items: ReviewItem[];
  cur: string | null;
  /** Apply the next decision to the rest of the group too. */
  all: boolean;
  undo: ReviewSnapshot[];
  sig?: string;
  /** Decision being previewed on hover/focus. */
  hov: string | null;
  /** Everything is decided: the completion card replaces the document. */
  fin: boolean;
  finAt?: number;
  setAt?: number;
}

/** What the result and manifest pages need to know about a batch, including archived ones. */
export interface BatchSummary {
  title: string;
  n: number;
  out: string;
  phase: BatchPhase;
  files: ManifestFile[];
  groups?: { id: string; t: string }[];
  manifestFilter?: ManifestFilter;
  manifestLimit?: number;
}
export type ManifestFile = Pick<BatchFile, "i" | "id" | "ext" | "st"> &
  Partial<Pick<BatchFile, "outputName" | "ver" | "reason" | "g" | "man" | "partial" | "sha">>;

export interface Batch extends BatchSummary {
  files: BatchFile[];
  groups: ExceptionGroup[];
  mode: BatchMode;
  preset: string;
  modelId: string | null;
  imgAct: ImageAction;
  autoPub: boolean;
  keep: KeepDays;
  paused: boolean;
  tab: BatchTab;
  ff: FileFilter;
  q: string;
  lim?: number;
  /** Replacements applied automatically after output validation found residue. */
  widened?: number;
  rv?: ReviewState;
  /** Set when the batch runs on the real engine. */
  live?: LiveBatch;
}

export interface LiveBatch {
  /** Engine batch id; empty until the import returned. */
  id: string;
  /** Engine file id of each row. */
  jobs: string[];
  /** Absolute output folder. */
  outputDir: string;
}

export type KeepDays = "7" | "30" | "0";

/* ── History and navigation ───────────────────────────────────────────────── */

export type HistoryStatus = "done" | "warn" | "run" | "keep" | "idle" | "stop" | "part";

export interface HistoryEntry {
  /** Stable identity; live entries use fixed ids. */
  id: string;
  k: "batch" | "file";
  name: string;
  ext?: string;
  meta: string;
  st: HistoryStatus;
  stl: string;
  when: string;
  /** Entry of a task that is still running in its slot. */
  live?: "batch" | "single";
  saved?: Single | Batch | BatchSummary;
  undo?: DecisionSnapshot[];
  /** A single document parked to make room for another one. */
  park?: boolean;
}

export interface HistoryResult {
  entry: HistoryEntry;
  /** Null while looking at the result of the live batch. */
  data: Single | BatchSummary | null;
  phase: "result" | "manifest";
}

export type View = "home" | "history" | "single" | "batch" | "presets" | "plugins" | "settings";

export interface NavEntry {
  view: View;
  presetTab: PresetTab;
  presetSel: string;
  rulesetSel: string | null;
  modelSel: string | null;
}

/* ── Settings and updates ─────────────────────────────────────────────────── */

export type ThemePref = "light" | "dark" | "system";
export type RedactStyle =
  | "black" | "gray" | "mosaic" | "stripe"
  | "heart" | "paw" | "snow" | "seal" | "star" | "dot" | "rainbow" | "plaid";

export interface Settings {
  redact: RedactStyle;
  tier: "comfort" | "std";
  autoUpd: boolean;
  keep: KeepDays;
  closeAct: "tray" | "quit";
  /** Use a model's GPU build when it is downloaded. */
  gpu: boolean;
}

export type UpdateStatus = "idle" | "checking" | "available" | "updating" | "latest";
export interface UpdateState {
  current: string;
  latest: string | null;
  status: UpdateStatus;
}
