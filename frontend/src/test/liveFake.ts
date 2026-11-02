import { STANDARD_CATEGORIES } from "../domain/categories";
import type { EngineCategory, EngineModelRuntime, EnginePlugin, EngineSettings, ConfigItem, ConfigVersion, EngineBatch, EngineFile, EngineFinding, EnginePagePreview, EnginePresetTest, EngineService, EngineUnit } from "../services/types";

export const cleanBatchStub = true;

export const unit = (seq: number, text: string, findings: Partial<EngineFinding>[], part = "body"): EngineUnit => ({
  seq, part, ordinal: seq, visibility: "visible", text,
  findings: findings.map((f, i) => ({ id: i + 1, start: 0, end: 1, category: "phone", source: "rule", score: 1, low: false, ...f })),
});

/** A scripted stand-in for the kernel: tests set what it reports and read what it was asked. */
export class FakeEngine implements EngineService {
  calls: string[] = [];
  rows: EngineFile[] = [];
  units: EngineUnit[] = [];
  afterManual: EngineUnit[] | null = null;
  decisions: { id: number; keep: boolean }[] | null = null;
  published: { partial: boolean; dir?: string } | null = null;
  started: Parameters<EngineService["start"]>[0] | null = null;
  manual: { job: string; seq: number; start: number; end: number; category: string } | null = null;
  unlocked: [string, string][] = [];
  previews: Partial<Record<number, EnginePagePreview>> = {};
  previewFails: Error | null = null;
  startFails: Error | null = null;
  importFails: Error | null = null;
  publishFails: Error | null = null;
  unlockFails: Error | null = null;
  resumeFails: Error | null = null;
  private handler: ((b: EngineBatch) => void) | null = null;
  private quick = false;

  row(i: number, state: string, extra: Partial<EngineFile> = {}): EngineFile {
    return { id: "j" + (i + 1), name: "f" + String(i + 1).padStart(4, "0"), state, status: state, format: "txt", pending: 0, findings: 0, ...extra };
  }
  private batch(): EngineBatch {
    return { id: "b0001", kind: this.quick ? "quick" : "batch", phase: "running", total: this.rows.length, counts: {}, delivered: 0, exit_code: 2, files: this.rows.slice(0, 200) };
  }
  /** Moves the single file to a state and tells the listener, as the kernel's event does. */
  async emit(state: string, extra: Partial<EngineFile> = {}) {
    this.rows = [this.row(0, state, extra)];
    await this.emitBatch();
  }
  async emitBatch() {
    this.handler?.(this.batch());
    await new Promise((r) => setTimeout(r, 10));
  }

  async info() { return { name: "Tacet", version: "0", ready: true }; }
  async pickFiles() { return []; }
  async pickFolder() { return "/picked"; }
  async defaultOutputDir() { return "/Users/x/Desktop/Tacet 已脱敏"; }
  workspace = { dir: "/private/tacet/workspace", bytes: 2.5 * 1024 ** 3 };
  workspaceFails: Error | null = null;
  async workspaceInfo() { this.calls.push("workspaceInfo"); if (this.workspaceFails) throw this.workspaceFails; return this.workspace; }
  async openWorkspace() { this.calls.push("openWorkspace"); if (this.workspaceFails) throw this.workspaceFails; }
  importedPreset: string | undefined;
  async importFiles(a: { files: string[]; quick: boolean; preset?: string }) {
    this.importedPreset = a.preset;
    this.calls.push("import:" + (a.quick ? "quick" : "batch") + ":" + a.files.length);
    if (this.importFails) throw this.importFails;
    this.quick = a.quick;
    if (!this.rows.length) this.rows = a.files.map((_, i) => this.row(i, "preflighting"));
    return this.batch();
  }
  unfinishedList: EngineBatch[] = [];
  async unfinished() { this.calls.push("unfinished"); return this.unfinishedList; }
  async preflight() { this.calls.push("preflight"); return this.batch(); }
  async start(a: Parameters<EngineService["start"]>[0]) {
    this.calls.push("start");
    this.started = a;
    if (this.startFails) throw this.startFails;
  }
  async status() { return this.batch(); }
  async files(_b: string, offset: number, limit: number) {
    this.calls.push("files:" + offset);
    return this.rows.slice(offset, offset + limit);
  }
  async publish(_b: string, partial: boolean, dir?: string) {
    this.calls.push("publish");
    if (this.publishFails) throw this.publishFails;
    this.published = { partial, dir };
  }
  async pause() { this.calls.push("pause"); }
  async resume() { this.calls.push("resume"); if (this.resumeFails) throw this.resumeFails; }
  async cancel() { this.calls.push("cancel"); }
  async clean() { this.calls.push("clean"); }
  async retry() { this.calls.push("retry"); }
  async unlock(job: string, password: string) {
    this.calls.push("unlock");
    this.unlocked.push([job, password]);
    if (this.unlockFails) throw this.unlockFails;
  }
  async documentUnits(_job: string, after: number) { return this.units.filter((u) => u.seq > after); }
  async pagePreview(job: string, page: number, _dpi: number) {
    this.calls.push("preview:" + page);
    if (this.previewFails) throw this.previewFails;
    const p = this.previews[page];
    if (!p) throw new Error("PREVIEW_FAILED");
    return { ...p, job };
  }
  stored: EngineSettings = { closeBehavior: "tray", theme: "system", redactStyle: "black", resourceTier: "comfort", retentionDays: 7, autoUpdate: false };
  pluginList: EnginePlugin[] = [];
  async plugins() { return this.pluginList; }
  modelList: EngineModelRuntime[] = [];
  async models() { this.calls.push("models"); return this.modelList.map((m) => ({ ...m })); }
  /** The engine's category catalog: the standard vocabulary unless a test changes it, then the stored user categories. */
  categoryList: EngineCategory[] = STANDARD_CATEGORIES.map((c) => ({ ...c }));
  async categories() {
    this.calls.push("categories");
    const user = this.items.filter((i) => i.kind === "category").map((i): EngineCategory => {
      const c = JSON.parse(i.content) as { placeholder: string; color?: string };
      return { id: i.id, name: i.name, placeholder: c.placeholder, ...(c.color ? { color: c.color } : {}), manual: true, origin: "custom", version: i.version };
    });
    return [...this.categoryList.map((c) => ({ ...c })), ...user];
  }
  modelFails: Error | null = null;
  /** A model, or the build part of the model whose accelerated build it is. */
  private model(id: string) { return (this.modelList.find((m) => m.id === id) ?? this.modelList.find((m) => m.accel?.id === id)?.accel)! as { state: string; code: string; downloaded: number }; }
  async downloadModel(id: string) {
    this.calls.push("downloadModel:" + id);
    if (this.modelFails) throw this.modelFails;
    Object.assign(this.model(id), { state: "downloading", code: "" });
  }
  async pauseModel(id: string) { this.calls.push("pauseModel:" + id); Object.assign(this.model(id), { state: "paused" }); }
  async discardModel(id: string) { this.calls.push("discardModel:" + id); Object.assign(this.model(id), { state: "pending", code: "", downloaded: 0 }); }
  async prepareModel(id: string) { this.calls.push("prepareModel:" + id); Object.assign(this.model(id), { state: "preparing", code: "" }); }
  modelDirFails: Error | null = null;
  async openModelDirectory(id: string) { this.calls.push("openModelDirectory:" + id); if (this.modelDirFails) throw this.modelDirFails; }
  settingsFails: Error | null = null;
  async settings() { return { ...this.stored }; }
  async saveSettings(s: EngineSettings) {
    this.calls.push("saveSettings");
    if (this.settingsFails) throw this.settingsFails;
    this.stored = { ...s };
    return { ...s };
  }
  saved: { name: string; text: string } | null = null;
  openText = "";
  async saveConfigFile(name: string, text: string) { this.saved = { name, text }; return true; }
  async openConfigFile() { return this.openText; }
  items: ConfigItem[] = [];
  configFails: Error | null = null;
  async configList(kind: string) { this.calls.push("configList:" + kind); return this.items.filter((i) => i.kind === kind); }
  async configCreate(kind: string, name: string, note: string, content: string) {
    this.calls.push("configCreate:" + kind);
    if (this.configFails) throw this.configFails;
    if (this.items.some((i) => i.kind === kind && i.name === name) || (kind === "category" && this.categoryList.some((c) => c.name === name))) throw new Error("CONFIG_NAME_CONFLICT");
    const it: ConfigItem = { id: "cfg_" + (this.items.length + 1), kind, name, note, version: 1, readOnly: false, content };
    this.items.push(it);
    return it;
  }
  async configSave(id: string, expected: number, name: string, note: string, content: string) {
    this.calls.push("configSave:" + id);
    if (this.configFails) throw this.configFails;
    const it = this.items.find((i) => i.id === id)!;
    if (it.version !== expected) throw new Error("VERSION_CONFLICT");
    const changed = it.content !== content;
    Object.assign(it, { name, note, content, version: changed ? it.version + 1 : it.version });
    return { ...it };
  }
  async configCopy(id: string, name: string) { return this.configCreate(this.items.find((i) => i.id === id)!.kind, name, "", this.items.find((i) => i.id === id)!.content); }
  async configDelete(id: string, expected: number) {
    this.calls.push("configDelete:" + id);
    if (this.configFails) throw this.configFails;
    const it = this.items.find((i) => i.id === id)!;
    if (it.version !== expected) throw new Error("VERSION_CONFLICT");
    this.items = this.items.filter((i) => i.id !== id);
  }
  history: Record<string, { v: ConfigVersion; content: string }[]> = {};
  async configHistory(id: string) { return (this.history[id] ?? []).map((h) => h.v); }
  async configRollback(id: string, expected: number, restore: number) {
    this.calls.push("configRollback:" + id + ":" + restore);
    const it = this.items.find((i) => i.id === id)!;
    if (it.version !== expected) throw new Error("VERSION_CONFLICT");
    it.content = this.history[id]!.find((h) => h.v.number === restore)!.content;
    it.version++;
    return { ...it };
  }
  testResult: EnginePresetTest = { hits: [], rulesOnly: true };
  testFails: Error | null = null;
  tested: { preset: string; text: string } | null = null;
  async testPreset(preset: string, text: string) {
    this.calls.push("testPreset:" + preset);
    this.tested = { preset, text };
    if (this.testFails) throw this.testFails;
    return this.testResult;
  }
  async decide(_job: string, ds: { id: number; keep: boolean }[]) {
    this.calls.push("decide");
    this.decisions = ds;
    // the kernel redoes the file from resolution as soon as a decision is stored
    this.rows = this.rows.map((r) => (r.state === "awaiting_review" || r.state === "ready_to_publish" ? { ...r, state: "resolving", pending: 0 } : r));
  }
  skipped: string[] = [];
  skipFails: Error | null = null;
  async skip(job: string) {
    this.calls.push("skip:" + job);
    if (this.skipFails) throw this.skipFails;
    this.skipped.push(job);
  }
  confirmed: string[] = [];
  confirmFails: Error | null = null;
  async confirm(job: string) {
    this.calls.push("confirm");
    if (this.confirmFails) throw this.confirmFails;
    this.confirmed.push(job);
  }
  withdrawn: number[] = [];
  withdrawFails: Error | null = null;
  afterWithdraw: EngineUnit[] | null = null;
  async withdrawManual(_job: string, finding: number) {
    this.calls.push("withdraw:" + finding);
    if (this.withdrawFails) throw this.withdrawFails;
    this.withdrawn.push(finding);
    if (this.afterWithdraw) this.units = this.afterWithdraw;
  }
  async addManual(job: string, seq: number, start: number, end: number, category: string) {
    this.calls.push("manual");
    this.manual = { job, seq, start, end, category };
    if (this.afterManual) this.units = this.afterManual;
    return 12;
  }
  onBatchChanged(h: (b: EngineBatch) => void) { this.handler = h; return () => { this.handler = null; }; }
  onFilesDropped() { return () => {}; }
}

export const rasterPage = (page: number, pages: number, boxes: { finding: number; rect: number[] }[]): EnginePagePreview => ({
  job: "j1", page, pages, width: 600, height: 800, image: "0123456789abcdef0123456" + page, boxes: boxes.map((b) => ({ ...b, category: "phone" })), unmapped: [],
});
