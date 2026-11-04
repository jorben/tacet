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
  /** Every decision call, and what the kernel keeps of them across a restart: documents read back with them. */
  decisionLog: { job: string; ds: { id: number; keep: boolean }[] }[] = [];
  private kernelDecisions = new Map<number, boolean>();
  decideFails: Error | null = null;
  decideGate: Promise<void> | null = null;
  /** The next read of a document fails, once, as a store that is briefly unavailable. */
  documentFails: Error | null = null;
  /** The answer to a status call is late, as the real call is while the kernel is busy. */
  statusGate: Promise<void> | null = null;
  published: { partial: boolean; dir?: string } | null = null;
  started: Parameters<EngineService["start"]>[0] | null = null;
  manual: { job: string; seq: number; start: number; end: number; category: string } | null = null;
  unlocked: [string, string][] = [];
  previews: Partial<Record<number, EnginePagePreview>> = {};
  previewFails: Error | null = null;
  startFails: Error | null = null;
  /** The next start waits for this, and fails with `startFails` if a test set it by then; later ones do not wait. */
  startGate: Promise<void> | null = null;
  /** The next read of rows, and of a document, waits for this. */
  filesGate: Promise<void> | null = null;
  documentGate: Promise<void> | null = null;
  pauseFails: Error | null = null;
  importFails: Error | null = null;
  publishFails: Error | null = null;
  unlockFails: Error | null = null;
  resumeFails: Error | null = null;
  private handler: ((b: EngineBatch) => void) | null = null;
  /** The batch the rows belong to. Others keep theirs in `kept` while they are parked or set aside. */
  current = "b0001";
  private count = 0;
  kept = new Map<string, EngineFile[]>();
  /** The batches that were started, and whether publishing was authorized. */
  private begun = new Map<string, boolean>();
  private beforePause = new Map<string, EngineFile[]>();
  /** The kind each batch was imported as. */
  private kinds = new Map<string, "quick" | "batch">();
  /** How many batches were started. */
  begunCount() { return this.begun.size; }
  resumed: string[] = [];
  cancelled: string[] = [];
  cleaned: string[] = [];
  private rowsOf(id: string): EngineFile[] { return id === this.current ? this.rows : this.kept.get(id) ?? []; }
  private setRows(id: string, rows: EngineFile[]) { if (id === this.current) this.rows = rows; else this.kept.set(id, rows); }
  private waiting(id: string): boolean {
    if (!this.begun.has(id)) return false; // a draft is never parked
    const granted = this.begun.get(id)!;
    return this.rowsOf(id).every((r) => ["awaiting_review", "blocked", "failed", "paused", "locked", "completed", "partial", "skipped", "unprocessable", "cancelled"].includes(r.state) || (r.state === "ready_to_publish" && !granted));
  }
  private statusOf(id: string): EngineBatch {
    const rows = this.rowsOf(id);
    const active = rows.filter((r) => ["preflighting", "queued", "extracting_detecting", "resolving", "exporting", "validating", "publishing"].includes(r.state)).length;
    return { id, kind: this.kinds.get(id) ?? "batch", phase: "running", total: rows.length, counts: {}, delivered: rows.filter((r) => r.state === "completed").length, exit_code: 2, files: rows.slice(0, 200), active, parked: this.waiting(id), started: this.begun.has(id) };
  }
  /** An event about any batch, as the kernel sends for one that is parked too. */
  async emitFor(id: string, rows: EngineFile[]) {
    this.setRows(id, rows);
    this.handler?.(this.statusOf(id));
    await new Promise((r) => setTimeout(r, 10));
  }

  row(i: number, state: string, extra: Partial<EngineFile> = {}): EngineFile {
    return { id: "j" + (i + 1), name: "f" + String(i + 1).padStart(4, "0"), state, status: state, format: "txt", pending: 0, findings: 0, ...extra };
  }
  private batch(): EngineBatch { return this.statusOf(this.current); }
  statusFor(id: string): EngineBatch { return this.statusOf(id); }
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
    this.count++;
    if (this.count > 1) { // the rows of the first task are what a test scripted; later ones start afresh unless it scripted those too
      this.kept.set(this.current, this.rows);
      this.current = "b" + String(this.count).padStart(4, "0");
      this.rows = this.nextRows ?? a.files.map((_, i) => this.row(i, "preflighting"));
      this.nextRows = null;
    }
    this.kinds.set(this.current, a.quick ? "quick" : "batch");
    if (!this.rows.length) this.rows = a.files.map((_, i) => this.row(i, "preflighting"));
    return this.batch();
  }
  nextRows: EngineFile[] | null = null;
  unfinishedList: EngineBatch[] = [];
  async unfinished() { this.calls.push("unfinished"); return this.unfinishedList; }
  async preflight() { this.calls.push("preflight"); return this.batch(); }
  async start(a: Parameters<EngineService["start"]>[0]) {
    this.calls.push("start");
    this.started = a;
    const gate = this.startGate;
    if (gate) { this.startGate = null; await gate; }
    if (this.startFails) { const err = this.startFails; if (gate) this.startFails = null; throw err; }
    this.begun.set(a.batch, a.authorize);
  }
  async status(b?: string) {
    if (this.statusGate) await this.statusGate;
    return b ? this.statusOf(b) : this.batch();
  }
  async files(b: string, offset: number, limit: number) {
    this.calls.push("files:" + offset);
    const gate = this.filesGate;
    if (gate) { this.filesGate = null; await gate; }
    return this.rowsOf(b).slice(offset, offset + limit);
  }
  publishedBatch = "";
  async publish(b: string, partial: boolean, dir?: string) {
    this.calls.push("publish");
    if (this.publishFails) throw this.publishFails;
    this.published = { partial, dir };
    this.publishedBatch = b;
  }
  async pause(b?: string) {
    this.calls.push("pause");
    if (this.pauseFails) throw this.pauseFails;
    if (!b) return;
    this.beforePause.set(b, this.rowsOf(b));
    this.setRows(b, this.rowsOf(b).map((r) => ({ ...r, state: "paused" })));
  }
  async resume(b?: string) {
    this.calls.push("resume");
    if (this.resumeFails) throw this.resumeFails;
    if (!b) return;
    this.resumed.push(b);
    const was = this.beforePause.get(b);
    if (was) { this.setRows(b, was); this.beforePause.delete(b); }
  }
  async cancel(b?: string) {
    this.calls.push("cancel");
    if (!b) return;
    this.cancelled.push(b);
    this.setRows(b, this.rowsOf(b).map((r) => ({ ...r, state: "cancelled" })));
  }
  async clean(b?: string) { this.calls.push("clean"); if (b) this.cleaned.push(b); }
  retried: [string, string[]][] = [];
  /** As the kernel: a failed file runs again, a blocked one too unless it waits for a password or the original changed. */
  async retry(b: string, only: string[] = []) {
    this.calls.push("retry");
    this.retried.push([b, only]);
    this.setRows(b, this.rowsOf(b).map((r) => (r.state === "failed" || (r.state === "blocked" && !["password_required", "PASSWORD_INCORRECT", "INPUT_CHANGED"].includes(r.detail ?? ""))) ? { ...r, state: "queued" } : r));
  }
  async unlock(job: string, password: string) {
    this.calls.push("unlock");
    this.unlocked.push([job, password]);
    if (this.unlockFails) throw this.unlockFails;
  }
  async documentUnits(_job: string, after: number) {
    const gate = this.documentGate;
    if (gate) { this.documentGate = null; await gate; }
    if (this.documentFails) { const err = this.documentFails; this.documentFails = null; throw err; }
    return this.units.filter((u) => u.seq > after).map((u) => ({ ...u, findings: (u.findings ?? []).map((f) => (this.kernelDecisions.has(f.id) ? { ...f, decision: this.kernelDecisions.get(f.id) ? "keep" : "redact" } : f)) }));
  }
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
  async decide(job: string, ds: { id: number; keep: boolean }[]) {
    this.calls.push("decide");
    if (this.decideGate) await this.decideGate;
    if (this.decideFails) throw this.decideFails;
    this.decisions = ds;
    this.decisionLog.push({ job, ds });
    ds.forEach((d) => this.kernelDecisions.set(d.id, d.keep));
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
