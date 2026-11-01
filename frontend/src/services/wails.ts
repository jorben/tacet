import { Application, Events, System, Window } from "@wailsio/runtime";
import * as G from "../../bindings/github.com/jorben/tacet/internal/gui/service";
import type { EnginePlugin, EngineSettings, ConfigItem, ConfigVersion, EngineBatch, EnginePagePreview, EngineFile, EngineService, HostKind, TacetService } from "./types";

/** Must match internal/gui/window.go EventCloseRequested. */
export const EVENT_CLOSE_REQUESTED = "tacet:close-requested";
/** Must match internal/gui/tray.go EventQuitRequested. */
export const EVENT_QUIT_REQUESTED = "tacet:quit-requested";
/** Must match internal/gui/service.go EventBatchChanged and EventFilesDropped. */
export const EVENT_BATCH_CHANGED = "tacet:batch-changed";
export const EVENT_FILES_DROPPED = "tacet:files-dropped";

function hostKind(): HostKind {
  if (System.IsMac()) return "darwin";
  if (System.IsWindows()) return "windows";
  return "linux";
}

/** The payload of a Wails event is the first element of `data`. */
const payload = <T,>(e: { data: unknown }): T => (Array.isArray(e.data) ? e.data[0] : e.data) as T;

const engine: EngineService = {
  async info() {
    const i = await G.AppInfo();
    return { name: i.name, version: i.version, ready: i.ready, problem: i.problem };
  },
  async pickFiles() { return (await G.PickFiles()) ?? []; },
  pickFolder: () => G.PickFolder(),
  defaultOutputDir: () => G.DefaultOutputDir(),
  importFiles: (a) => G.ImportFiles({ files: a.files, quick: a.quick, preset: a.preset ?? "", outputDir: a.outputDir ?? "" }) as Promise<EngineBatch>,
  async unfinished() { return ((await G.UnfinishedBatches()) ?? []) as EngineBatch[]; },
  preflight: (b) => G.Preflight(b) as Promise<EngineBatch>,
  start: (a) => G.Start({ batch: a.batch, preset: a.preset ?? "", outputDir: a.outputDir ?? "", authorize: a.authorize, allowPartial: a.allowPartial, mode: a.mode ?? "" }),
  async files(b, offset, limit) { return ((await G.BatchFiles(b, offset, limit)) ?? []) as EngineFile[]; },
  status: (b, limit) => G.BatchStatus(b, limit) as Promise<EngineBatch>,
  publish: (b, partial, dir) => G.PublishBatch(b, partial, dir ?? ""),
  pause: (b) => G.PauseBatch(b),
  resume: (b) => G.ResumeBatch(b),
  cancel: (b) => G.CancelBatch(b),
  clean: (b) => G.CleanBatch(b),
  retry: (b, files) => G.RetryFiles(b, files),
  unlock: (job, pw) => G.UnlockFile(job, pw),
  async documentUnits(job, after, limit) { return ((await G.DocumentUnits(job, after, limit)) ?? []) as any; },
  pagePreview: (job, page, dpi) => G.PagePreview(job, page, dpi) as Promise<EnginePagePreview>,
  async plugins() { return ((await G.Plugins()) ?? []) as EnginePlugin[]; },
  settings: () => G.Settings() as Promise<EngineSettings>,
  saveSettings: (s) => G.SaveSettings(s as never) as Promise<EngineSettings>,
  saveConfigFile: (n, t) => G.SaveConfigFile(n, t),
  openConfigFile: () => G.OpenConfigFile(),
  async configList(kind) { return ((await G.ConfigList(kind)) ?? []) as ConfigItem[]; },
  configCreate: (k, n, note, c) => G.ConfigCreate(k, n, note, c) as Promise<ConfigItem>,
  configSave: (id, v, n, note, c) => G.ConfigSave(id, v, n, note, c) as Promise<ConfigItem>,
  configCopy: (id, n) => G.ConfigCopy(id, n) as Promise<ConfigItem>,
  configDelete: (id, v) => G.ConfigDelete(id, v),
  async configHistory(id) { return ((await G.ConfigHistory(id)) ?? []) as ConfigVersion[]; },
  configRollback: (id, v, r) => G.ConfigRollback(id, v, r) as Promise<ConfigItem>,
  decide: (job, ds) => G.DecideFindings(job, ds as any),
  confirm: (job) => G.ConfirmFile(job),
  skip: (job) => G.SkipFile(job),
  addManual: (job, seq, start, end, category) => G.AddManualRedaction(job, seq, start, end, category),
  withdrawManual: (job, finding) => G.WithdrawManualRedaction(job, finding),
  onBatchChanged(handler) { return Events.On(EVENT_BATCH_CHANGED, (e) => handler(payload<EngineBatch>(e))); },
  onFilesDropped(handler) { return Events.On(EVENT_FILES_DROPPED, (e) => handler(payload<string[]>(e))); },
};

/** Adapter over the Wails runtime and the internal/gui binding. */
export const wailsService: TacetService = {
  engine,
  get host() {
    return hostKind();
  },
  async appInfo() {
    const info = await G.AppInfo();
    return { name: info.name, version: info.version };
  },
  async minimiseWindow() {
    await Window.Minimise();
  },
  async hideWindow() {
    await Window.Hide();
  },
  async quitApp() {
    await Application.Quit();
  },
  onQuitRequested(handler) {
    return Events.On(EVENT_QUIT_REQUESTED, () => handler());
  },
  onCloseRequested(handler) {
    const off = Events.On(EVENT_CLOSE_REQUESTED, () => handler());
    // Tell the host a handler exists; before this the native close behaviour stays.
    void G.CloseHandlerReady();
    return off;
  },
};
