// Keeps the schemes and rule sets on screen and the ones the kernel stores the same. The screens edit the store as
// before; this module loads what is stored when the application starts and writes every change back, so nothing a
// person sets up lives only in memory.
import {
  BUILTIN_MODEL_PRESET, BUILTIN_RULES_PRESET, modelFromItem, modelStateFromEngine, presetContents, presetFromItems, rulesetContent, rulesetFromItem, sameContent,
  schemeIdOf, versionLabel, type Model,
} from "../domain/liveConfig";
import type { Preset, Ruleset } from "../domain/types";
import { presetCategoryLabel } from "../domain/presets";
import { pluginFromEngine, settingsFromEngine, settingsToEngine, exportRuleset, exportScheme, importPackage, type Imported } from "../domain/liveConfig";
import { closeModal, openModal, toast } from "../overlay/store";
import { importNameClashes } from "./presets";
import { nextId } from "./timers";
import type { ConfigItem } from "../services/types";
import { getState, store } from "../store";
import { liveEngine } from "./live";

let synced = new Map<string, ConfigItem>();
/** Screen objects as last written or read; an object not changed since needs no comparison. */
let known = new WeakSet<object>();
const markKnown = (o: object | undefined): void => { if (o) known.add(o); };
let models: Model[] = [];
let modelTimer: ReturnType<typeof setTimeout> | undefined;
let loaded = false;
let timer: ReturnType<typeof setTimeout> | undefined;
let running: Promise<void> | null = null;
let again = false;
let unsubscribe: (() => void) | null = null;
let last: { presets: unknown; rulesets: unknown } = { presets: null, rulesets: null };

const FLUSH_DELAY_MS = 350;

/** Why a stored change was refused, in words. */
const CONFIG_ERRORS: Record<string, string> = {
  CONFIG_NAME_CONFLICT: "名称已被使用",
  VERSION_CONFLICT: "内容已在别处变化，已重新载入",
  INVALID_REQUEST: "内容不符合要求",
  ACTION_NOT_ALLOWED: "内建内容只读，或正被方案使用",
  RULE_LOOKAROUND_UNSUPPORTED: "不支持前后断言，请改用“前后边界”",
  NOT_FOUND: "对象已不存在，已重新载入",
};
/** The closed code a kernel call failed with. */
const errorCode = (e: unknown): string => String((e as { message?: string })?.message ?? e).replace(/^.*?([A-Z][A-Z0-9_]{2,})\b.*$/s, "$1");
export const configErrorText = (e: unknown): string => {
  const code = errorCode(e);
  return CONFIG_ERRORS[code] ?? "保存未完成（" + code + "）";
};

const liveOn = (): boolean => !!liveEngine();

/** The kernel's id of the scheme a screen scheme is stored as; the one the screens run with when nothing is chosen. */
export function engineSchemeId(name: string): string | undefined {
  const p = getState().presets.find((x) => x.name === name);
  return p?.stored?.id;
}

/** Reads everything stored and puts it on screen. */
export async function loadConfig(): Promise<void> {
  const e = liveEngine();
  if (!e) return;
  const [rs, sc, pr, mp] = await Promise.all(["ruleset", "scheme", "preset", "model_profile"].map((k) => e.configList(k)));
  synced = new Map([...rs, ...sc, ...pr, ...mp].map((i) => [i.id, i]));
  // the catalog decides the order (its first model is the default one); a profile without an entry keeps its place last
  const rts = mp.length ? await e.models().catch(() => []) : [];
  const order = (id: string) => { const i = rts.findIndex((r) => r.id === id); return i < 0 ? rts.length : i; };
  const mi = mp.map((it) => { const x = modelFromItem(it); return x && modelFromItem(it, rts.find((r) => r.id === x.model.id)); })
    .filter((x): x is NonNullable<typeof x> => !!x)
    .sort((a, b) => order(a.model.id) - order(b.model.id));
  models = mi.map((x) => x.model);
  const rulesets = rs.map(rulesetFromItem);
  const schemes = new Map(sc.map((s) => [s.id, s]));
  const presets: Preset[] = pr.map((p) => presetFromItems(p, schemes.get(schemeIdOf(p)), rulesets, models));
  await loadSettings().catch(() => {});
  await loadPlugins().catch(() => {});
  if (!presets.length) return; // nothing stored to show: the screens keep what they have
  // One office preset stays bound to its model while resources are being prepared.
  const homeId = mi.length ? BUILTIN_MODEL_PRESET : BUILTIN_RULES_PRESET;
  const home = presets.find((p) => p.stored?.id === homeId) ?? presets[0];
  store.update((d) => {
    d.rulesets = rulesets;
    d.presets = presets;
    d.models = mi.map((x) => x.plugin);
    d.presetSel = home?.id ?? "";
    d.rulesetSel = null;
    if (home) d.homePreset = home.name;
  });
  known = new WeakSet();
  getState().presets.forEach(markKnown);
  getState().rulesets.forEach(markKnown);
  loaded = true;
  last = { presets: getState().presets, rulesets: getState().rulesets };
  if (!unsubscribe) unsubscribe = store.subscribe(onStore);
  watchModels();
}

const MODEL_POLL_MS = 700;
const modelBusy = (st: string): boolean => st === "downloading" || st === "verifying" || st === "loading";

/** Reads the download state of every model; while one is busy, again shortly. A model that became ready says so. */
export async function refreshModels(): Promise<void> {
  const e = liveEngine();
  if (!e) return;
  const rts = await e.models().catch(() => null);
  if (!rts) return;
  const became: string[] = [];
  store.update((d) => {
    d.models.forEach((m) => {
      const rt = rts.find((r) => r.id === m.id);
      if (!rt) return;
      const next = modelStateFromEngine(rt);
      if (m.status !== "ready" && next.status === "ready" && modelBusy(m.status)) became.push(m.name);
      const a = m.runtime?.accel, na = next.runtime?.accel;
      if (a && na && a.status !== "ready" && na.status === "ready" && modelBusy(a.status)) became.push(m.name + " GPU 版本");
      Object.assign(m, next);
      if (rt.bytes) m.weightMiB = Math.round(rt.bytes / 2 ** 20);
    });
  });
  became.forEach((name) => toast(name + " 已就绪" + (getState().modelPending ? "，可以继续处理" : "，可以离线使用")));
  watchModels();
}

/** Keeps the model states fresh while a download, verification or trial runs. */
export function watchModels(): void {
  clearTimeout(modelTimer);
  if (!loaded || !getState().models.some((m) => modelBusy(m.status) || modelBusy(m.runtime?.accel?.status ?? ""))) return;
  modelTimer = setTimeout(() => void refreshModels(), MODEL_POLL_MS);
}

export function stopConfigSync(): void {
  clearTimeout(modelTimer);
  unsubscribe?.();
  unsubscribe = null;
  clearTimeout(timer);
  loaded = false;
}

function onStore(): void {
  const s = getState();
  if (!loaded || (s.presets === last.presets && s.rulesets === last.rulesets)) return;
  last = { presets: s.presets, rulesets: s.rulesets };
  clearTimeout(timer);
  timer = setTimeout(() => void flushConfig(), FLUSH_DELAY_MS);
}

/** Writes pending changes now (one run at a time; a change during a run schedules another). */
export function flushConfig(): Promise<void> {
  clearTimeout(timer);
  if (!liveOn() || !loaded) return Promise.resolve();
  if (running) { again = true; return running; }
  running = (async () => {
    try {
      do {
        again = false;
        await writeChanges();
      } while (again);
    } catch (err) {
      toast(configErrorText(err));
      await loadConfig().catch(() => {});
    } finally {
      running = null;
    }
  })();
  return running;
}

const keep = (it: ConfigItem): void => { synced.set(it.id, it); };

async function writeChanges(): Promise<void> {
  const e = liveEngine()!;

  // 1. what was deleted on screen
  const s0 = getState();
  const livePresets = new Set(s0.presets.map((p) => p.stored?.id).filter(Boolean));
  const liveSets = new Set(s0.rulesets.map((r) => r.stored?.id).filter(Boolean));
  for (const it of [...synced.values()]) {
    if (it.readOnly || it.kind !== "preset" || livePresets.has(it.id)) continue;
    await e.configDelete(it.id, it.version);
    synced.delete(it.id);
    const scheme = synced.get(schemeIdOf(it));
    if (scheme && !scheme.readOnly) { await e.configDelete(scheme.id, scheme.version); synced.delete(scheme.id); }
  }
  for (const it of [...synced.values()]) {
    if (it.readOnly || it.kind !== "ruleset" || liveSets.has(it.id)) continue;
    await e.configDelete(it.id, it.version);
    synced.delete(it.id);
  }

  // 2. rule sets (a preset may refer to one that is new)
  for (const rs of getState().rulesets) {
    if (rs.builtin || rs.plugin || known.has(rs)) continue;
    const content = rulesetContent(rs);
    const old = rs.stored ? synced.get(rs.stored.id) : undefined;
    let out: ConfigItem;
    if (!old) {
      out = await e.configCreate("ruleset", rs.name, "", content);
      const oldId = rs.id;
      store.update((d) => {
        const x = d.rulesets.find((r) => r.id === oldId);
        if (x) { x.id = out.id; x.ver = versionLabel(out); x.stored = { id: out.id, version: out.version }; }
        d.presets.forEach((p) => { if (p.rules.includes(oldId)) p.rules = p.rules.map((r) => (r === oldId ? out.id : r)); });
        if (d.rulesetSel === oldId) d.rulesetSel = out.id;
      });
    } else if (!sameContent(old.content, content) || old.name !== rs.name) {
      out = await e.configSave(old.id, old.version, rs.name, old.note, content);
      store.update((d) => {
        const x = d.rulesets.find((r) => r.id === rs.id);
        if (x) { x.ver = versionLabel(out); x.stored = { id: out.id, version: out.version }; }
      });
    } else {
      if (rs.stored?.version !== old.version) store.update((d) => { const x = d.rulesets.find((r) => r.id === rs.id); if (x) { x.stored = { id: old.id, version: old.version }; x.ver = versionLabel(old); } });
      markKnown(getState().rulesets.find((r) => r.id === rs.id));
      continue;
    }
    keep(out);
    markKnown(getState().rulesets.find((r) => r.id === out.id));
  }

  // 3. schemes (each is a scheme object and a preset object)
  const dropped = new Set<string>();
  for (const p of getState().presets) {
    if (p.builtin || known.has(p)) continue;
    const s = getState();
    const w = presetContents(p, s.rulesets as Ruleset[], models, {
      scheme: p.stored?.schemeId ? synced.get(p.stored.schemeId) : undefined,
      preset: p.stored ? synced.get(p.stored.id) : undefined,
    });
    w.dropped.forEach((c) => dropped.add(c));
    let scheme = p.stored?.schemeId ? synced.get(p.stored.schemeId) : undefined;
    let preset = p.stored ? synced.get(p.stored.id) : undefined;
    let changed = false;
    if (!scheme) {
      scheme = await e.configCreate("scheme", "方案 " + p.id, "", w.scheme);
      keep(scheme);
      changed = true;
    } else if (!sameContent(scheme.content, w.scheme)) {
      scheme = await e.configSave(scheme.id, scheme.version, scheme.name, scheme.note, w.scheme);
      keep(scheme);
      changed = true;
    }
    const content = w.preset(scheme.id, scheme.version);
    if (!preset) {
      preset = await e.configCreate("preset", p.name, "", content);
      changed = true;
    } else if (!sameContent(preset.content, content) || preset.name !== p.name) {
      preset = await e.configSave(preset.id, preset.version, p.name, preset.note, content);
      changed = true;
    }
    keep(preset);
    if (changed || p.stored?.id !== preset.id || p.stored.version !== preset.version) {
      const uiId = p.id;
      const sc = scheme;
      const pr = preset;
      store.update((d) => {
        const x = d.presets.find((q) => q.id === uiId);
        if (!x) return;
        x.stored = { id: pr.id, version: pr.version, schemeId: sc.id, schemeVersion: sc.version };
        x.ver = versionLabel(pr);
        x.id = pr.id;
        if (d.presetSel === uiId) d.presetSel = pr.id;
      });
    }
    markKnown(getState().presets.find((q) => q.id === preset!.id));
  }
  if (dropped.size) toast("以下类别没有规则或模型检测，未写入方案：" + [...dropped].map(presetCategoryLabel).join("、"));
  last = { presets: getState().presets, rulesets: getState().rulesets };
}

/* ── versions ──────────────────────────────────────────────────────────────── */

const ago = (unix: number): string => {
  const m = Math.max(0, Math.round((Date.now() / 1000 - unix) / 60));
  return m < 1 ? "刚刚" : m < 60 ? m + " 分钟前" : m < 1440 ? Math.round(m / 60) + " 小时前" : Math.round(m / 1440) + " 天前";
};

/** Opens the version list of the selected object, read from the kernel. */
export async function openVersions(kind: "preset" | "ruleset"): Promise<void> {
  const e = liveEngine()!;
  await flushConfig();
  const s = getState();
  const target = kind === "ruleset" ? s.rulesets.find((r) => r.id === s.rulesetSel) ?? s.rulesets[0] : s.presets.find((p) => p.id === s.presetSel) ?? s.presets[0];
  const id = target?.stored?.id;
  if (!target || !id) { toast("这个对象还没有保存过"); return; }
  try {
    const versions = await e.configHistory(id);
    store.update((d) => {
      const list = (kind === "ruleset" ? d.rulesets : d.presets) as (Ruleset | Preset)[];
      const x = list.find((o) => o.id === target.id);
      if (!x) return;
      const cur = x.stored?.version;
      x.edited = versions.find((v) => v.number === cur) ? ago(versions.find((v) => v.number === cur)!.savedAt) : "";
      x.hist = versions.filter((v) => v.number !== cur).map((v) => ({ ver: "v" + v.number, when: ago(v.savedAt), data: undefined as never }));
    });
    openModal({ type: "versions", kind });
  } catch (err) { toast(configErrorText(err)); }
}

/** Saves an earlier version's content as a new version. */
export async function rollbackLive(kind: "preset" | "ruleset", index: number): Promise<string | null> {
  const e = liveEngine()!;
  const s = getState();
  const target = kind === "ruleset" ? s.rulesets.find((r) => r.id === s.rulesetSel) ?? s.rulesets[0] : s.presets.find((p) => p.id === s.presetSel) ?? s.presets[0];
  const h = target?.hist?.[index];
  if (!target?.stored || !h) return null;
  try {
    await e.configRollback(target.stored.id, target.stored.version, Number(h.ver.slice(1)));
    await loadConfig();
    return h.ver;
  } catch (err) {
    toast(configErrorText(err));
    return null;
  }
}

/** The name a screen scheme is chosen by when a task starts. */
export const presetEngineId = (name: string): string | undefined => engineSchemeId(name);

/* ── exchange files ────────────────────────────────────────────────────────── */

const fileName = (name: string): string => name.replace(/[\\/:*?"<>|]/g, "_").slice(0, 80) + ".json";

/** Writes a rule set or scheme to a file the person chooses. The model and any ids stay out. */
export async function exportLive(kind: "preset" | "ruleset", id: string): Promise<void> {
  const e = liveEngine()!;
  await flushConfig();
  const s = getState();
  try {
    let name: string;
    let text: string;
    if (kind === "ruleset") {
      const rs = s.rulesets.find((r) => r.id === id);
      if (!rs) return;
      name = rs.name;
      text = exportRuleset(rs);
    } else {
      const p = s.presets.find((x) => x.id === id);
      if (!p) return;
      name = p.name;
      text = exportScheme(p, s.rulesets, models);
    }
    if (await e.saveConfigFile(fileName(name), text)) toast("已导出「" + name + "」（不含模型）");
  } catch (err) { toast(configErrorText(err)); }
}

type Parsed = Exclude<Imported, { error: unknown }>;
let pending: Parsed | null = null;

const IMPORT_ERRORS = { format: "不是 Tacet 的配置文件", kind: "文件类型不符", content: "文件内容不符合要求", rulesets: "缺少方案所用的规则集" } as const;

/** Reads a chosen file; a name already taken asks for another one, nothing is overwritten or merged. */
export async function importLive(kind: "preset" | "ruleset"): Promise<void> {
  const e = liveEngine()!;
  try {
    const text = await e.openConfigFile();
    if (!text) return;
    const got = importPackage(text, kind, getState().rulesets, models);
    if ("error" in got) {
      toast(IMPORT_ERRORS[got.error] + (got.detail ? "：" + got.detail : ""));
      return;
    }
    pending = got as Parsed;
    if (importNameClashes(kind, got.name)) openModal({ type: "importConflict", kind, name: got.name });
    else finishImportLive(kind, got.name);
  } catch (err) { toast(configErrorText(err)); }
}

export function finishImportLive(kind: "preset" | "ruleset", name: string): void {
  const got = pending;
  pending = null;
  closeModal();
  if (!got || (got.kind === "ruleset") !== (kind === "ruleset")) return;
  store.update((d) => {
    if (got.kind === "ruleset") {
      const o: Ruleset = { ...got.ruleset, id: nextId("rs"), name, ver: "v1", edited: "刚刚" };
      d.rulesets.push(o);
      d.rulesetSel = o.id;
    } else {
      const o: Preset = { ...got.preset, id: nextId("p"), name, ver: "v1", edited: "刚刚" };
      d.presets.push(o);
      d.presetSel = o.id;
    }
  });
  toast("已导入「" + name + "」 · v1");
}

/* ── application preferences ───────────────────────────────────────────────── */

let settingsTimer: ReturnType<typeof setTimeout> | undefined;
let settingsLoaded = false;

/** Reads the stored preferences onto the screen. */
export async function loadSettings(): Promise<void> {
  const e = liveEngine();
  if (!e) return;
  const { settings, theme } = settingsFromEngine(await e.settings());
  const dark = typeof window !== "undefined" && window.matchMedia?.("(prefers-color-scheme: dark)").matches;
  store.update((d) => {
    d.settings = settings;
    d.themePref = theme;
    d.theme = theme === "system" ? (dark ? "dark" : "light") : theme;
  });
  settingsLoaded = true;
}

/** Writes the preferences shortly after the last change. */
export function saveSettingsSoon(): void {
  if (!liveEngine() || !settingsLoaded) return;
  clearTimeout(settingsTimer);
  settingsTimer = setTimeout(() => void saveSettingsNow(), 250);
}

export async function saveSettingsNow(): Promise<void> {
  const e = liveEngine();
  if (!e || !settingsLoaded) return;
  clearTimeout(settingsTimer);
  const s = getState();
  try {
    await e.saveSettings(settingsToEngine(s.settings, s.themePref));
  } catch (err) {
    toast(configErrorText(err));
    await loadSettings().catch(() => {});
  }
}


/** The plugins this installation really runs replace the sample list. */
export async function loadPlugins(): Promise<void> {
  const e = liveEngine();
  if (!e) return;
  const list = (await e.plugins()).map(pluginFromEngine).filter((p) => p.role !== "模型");
  store.update((d) => { d.plugins = list; });
}
