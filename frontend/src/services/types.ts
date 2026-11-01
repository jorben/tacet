// Service boundary between the UI and the (not yet implemented) Go kernel.
// The UI only talks to this interface; today everything except the host/window calls is simulated.

export interface AppInfo {
  name: string;
  version: string;
}

/** Operating-system chrome the window is drawn with. */
export type HostKind = "browser" | "darwin" | "windows" | "linux";

/** One file of a batch as the engine reports it. `name` is the neutral identifier, never the source name. */
export interface EngineFile {
  id: string;
  name: string;
  state: string;
  status: string;
  detail?: string;
  format: string;
  pending: number;
  findings: number;
  categories?: Record<string, number> | null;
  output?: string;
  /** sha256 of the published file. */
  digest?: string;
  /** Waits for its own confirmation before it is exported (strict mode). */
  confirm?: boolean;
}

export interface EngineBatch {
  id: string;
  kind: string;
  phase: string;
  total: number;
  counts: Record<string, number> | null;
  delivered: number;
  exit_code: number;
  files: EngineFile[] | null;
  truncated?: boolean;
  /** The settings were frozen (the batch was started). */
  started?: boolean;
  /** Publishing was authorized. */
  authorized?: boolean;
}

export interface EngineFinding {
  id: number;
  start: number;
  end: number;
  category: string;
  source: string;
  score: number;
  low: boolean;
  /** "redact", "keep" or empty (no decision yet). */
  decision?: string;
}

/** One text unit of a document; start/end of its findings count UTF-16 code units of `text`. */
export interface EngineUnit {
  seq: number;
  part: string;
  ordinal: number;
  visibility: string;
  text: string;
  findings: EngineFinding[] | null;
  /** DOCX: where the unit is; numbers only (approximate page, ordinal of the nearest heading, table cell). */
  location?: { page_hint?: number; section?: number; table?: { index: number; row: number; col: number } } | null;
}

export interface EnginePagePreview {
  job: string;
  page: number;
  pages: number;
  width: number;
  height: number;
  /** Token of the rendered image; the picture is served at `/tacet-preview/<token>.png`. */
  image: string;
  boxes: { finding: number; rect: number[]; category: string; decision?: string }[] | null;
  unmapped: number[] | null;
}

/** A stored configuration object: rule set, scheme, preset or model profile. `content` is its JSON text. */
export interface ConfigItem {
  id: string;
  kind: string;
  name: string;
  note: string;
  version: number;
  readOnly: boolean;
  content: string;
  minConfidenceReset?: boolean;
}

export interface ConfigVersion {
  number: number;
  savedAt: number;
}

/** Application preferences as the kernel stores them. */
export interface EngineSettings {
  closeBehavior: string;
  theme: string;
  redactStyle: string;
  resourceTier: string;
  retentionDays: number;
  autoUpdate: boolean;
}

export interface EnginePlugin {
  id: string;
  version: string;
  roles: string[] | null;
  formats: string[] | null;
  builtin: boolean;
  enabled: boolean;
}

export interface EngineInfo {
  name: string;
  version: string;
  ready: boolean;
  problem?: string;
}

/** The real kernel behind the Wails host. Every call returns a closed error code on failure, never a path or content. */
export interface EngineService {
  info(): Promise<EngineInfo>;
  pickFiles(): Promise<string[]>;
  pickFolder(): Promise<string>;
  defaultOutputDir(): Promise<string>;
  importFiles(a: { files: string[]; quick: boolean; preset?: string; outputDir?: string }): Promise<EngineBatch>;
  preflight(batch: string): Promise<EngineBatch>;
  /** What a restart left behind: at most one quick task and one batch, paused where they stopped. */
  unfinished(): Promise<EngineBatch[]>;
  start(a: { batch: string; preset?: string; outputDir?: string; authorize: boolean; allowPartial: boolean; mode?: "guided" | "strict" }): Promise<void>;
  status(batch: string, limit: number): Promise<EngineBatch>;
  /** Rows [offset, offset+limit) of a batch, at most 200. */
  files(batch: string, offset: number, limit: number): Promise<EngineFile[]>;
  /** Authorize publishing; `outputDir` (optional) is the folder chosen for the results. */
  publish(batch: string, partial: boolean, outputDir?: string): Promise<void>;
  pause(batch: string): Promise<void>;
  resume(batch: string): Promise<void>;
  cancel(batch: string): Promise<void>;
  clean(batch: string): Promise<void>;
  retry(batch: string, files: string[]): Promise<void>;
  unlock(job: string, password: string): Promise<void>;
  documentUnits(job: string, after: number, limit: number): Promise<EngineUnit[]>;
  /** One page of a PDF waiting for review, rendered, with the place of each candidate. */
  pagePreview(job: string, page: number, dpi: number): Promise<EnginePagePreview>;
  /** Asks where to save an exchange file; false means cancelled. */
  saveConfigFile(suggested: string, text: string): Promise<boolean>;
  /** Asks for an exchange file; "" means cancelled. */
  openConfigFile(): Promise<string>;
  plugins(): Promise<EnginePlugin[]>;
  settings(): Promise<EngineSettings>;
  saveSettings(s: EngineSettings): Promise<EngineSettings>;
  configList(kind: string): Promise<ConfigItem[]>;
  configCreate(kind: string, name: string, note: string, content: string): Promise<ConfigItem>;
  /** `expected` is the version the caller last saw; a different stored version is a conflict. */
  configSave(id: string, expected: number, name: string, note: string, content: string): Promise<ConfigItem>;
  configCopy(id: string, name: string): Promise<ConfigItem>;
  configDelete(id: string, expected: number): Promise<void>;
  configHistory(id: string): Promise<ConfigVersion[]>;
  configRollback(id: string, expected: number, restore: number): Promise<ConfigItem>;
  decide(job: string, decisions: { id: number; keep: boolean }[]): Promise<void>;
  /** The per-file confirmation of strict mode: one file, never several at once. */
  confirm(job: string): Promise<void>;
  /** “跳过此文件”: one waiting file is left out of its batch and not exported. */
  skip(job: string): Promise<void>;
  addManual(job: string, seq: number, start: number, end: number, category: string): Promise<number>;
  /** Takes back a manual redaction (the finding addManual returned) with its decision. */
  withdrawManual(job: string, finding: number): Promise<void>;
  /** Called with a fresh status whenever a batch changed. Returns the unsubscribe function. */
  onBatchChanged(handler: (b: EngineBatch) => void): () => void;
  /** Called with absolute paths of files dropped on the window. */
  onFilesDropped(handler: (files: string[]) => void): () => void;
}

export interface TacetService {
  /** The real kernel; absent in a plain browser, where the UI runs on its simulation. */
  readonly engine?: EngineService;
  /** Product name and version (the one data capability that exists in internal/gui today). */
  appInfo(): Promise<AppInfo>;
  /** Where the UI runs: a plain browser (dev, tests) or a Wails desktop host. */
  readonly host: HostKind;
  /** Minimise the main window. No-op in a browser. */
  minimiseWindow(): Promise<void>;
  /** Hide the main window to the tray; work goes on. No-op in a browser. */
  hideWindow(): Promise<void>;
  /** End the application. No-op in a browser. */
  quitApp(): Promise<void>;
  /**
   * Called once the UI is ready to handle user-initiated window closes (title-bar button, Cmd+W).
   * The handler is invoked instead of closing the window; return value is ignored.
   * Returns a function that removes the handler.
   */
  onCloseRequested(handler: () => void): () => void;
  /** Called when the person chooses “退出” in the tray: an explicit quit. Returns the unsubscribe function. */
  onQuitRequested(handler: () => void): () => void;
}
