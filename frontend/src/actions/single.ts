// The single-file task: detection, review decisions, undo and export.
import { current, isDraft, type Draft } from "immer";
import { categoryName, categoryPlaceholder } from "../domain/categories";
import { extOf, locAt, outputKind } from "../domain/format";
import { presetModelId } from "../domain/presets";
import { buildDocument } from "../domain/sampleDocument";
import {
  addManualHit, createSingle, isPartialResult, orderedHitIds, sameValueHits, treatmentOf, type ManualSelection,
} from "../domain/single";
import type { CategoryId, ImageAction, Single, Treatment, ViewMode } from "../domain/types";
import { getState, store, type AppState } from "../store";
import { closeModal, closePop, openMenu, openModal, openPop, toast, type MenuEntry, type PopAnchor } from "../overlay/store";
import { nextId, nextRunId, stopSingleTimer, timers } from "./timers";
import { leaveBatchFile, reportLeftBatchFile } from "./batch";
import { enterBatchReview } from "./review";
import { needModel } from "./models";
import { openSubpage } from "./navigation";
import { addLiveManual, cancelLiveSingle, chooseOutputFolder, exportLiveSingle, withdrawLiveManual } from "./live";

const plain = <T,>(v: T): T => (isDraft(v) ? (current(v as Draft<T>) as T) : v);

/* ── Creating and processing ──────────────────────────────────────────────── */

export function newSingle(name: string, size: number | undefined, opt: Partial<Single> = {}): void {
  const s = getState();
  const preset = opt.preset || s.homePreset;
  const options: Partial<Single> = { modelId: presetModelId(s.presets, preset), ...opt };
  if ((!options.phase || options.phase === "processing") && !needModel(preset, () => newSingle(name, size, opt), name, options.modelId)) return;
  const runId = nextRunId();
  store.update((d) => {
    d.single = createSingle(name, extOf(name), size, { mode: d.docMode, preset: d.homePreset, modelId: null }, { ...options, runId });
    d.undo = [];
    d.view = "single";
  });
  if (getState().single!.phase === "processing") runSingleProcessing();
}

/** The task with this run id, wherever it currently sits (a parked task keeps running behind a batch file). */
function findRun(d: Draft<AppState>, runId: number): Single | null {
  if (d.single?.runId === runId) return d.single;
  if (d.behind?.single.runId === runId) return d.behind.single;
  return null;
}

/** Runs one two-step progress (read → detect, export → verify) and calls `done` when it completes. */
function runProgress(interval: number, speed: (s: Single) => number, done: (d: Draft<AppState>, s: Single) => void): void {
  stopSingleTimer();
  const runId = getState().single!.runId;
  timers.single = setInterval(() => {
    let stop = false;
    store.update((d) => {
      const s = findRun(d, runId);
      if (!s) { stop = true; return; }
      s.sp += speed(s);
      if (s.sp >= 100) {
        s.sp = 0;
        s.stage++;
        if (s.stage >= 2) { stop = true; done(d, s); }
      }
    });
    if (stop) stopSingleTimer();
  }, interval);
}

export function runSingleProcessing(): void {
  runProgress(70, (s) => (s.stage === 0 ? 9 : 4), (_d, s) => { s.phase = "review"; });
}

export function cancelSingle(): void {
  if (getState().single?.live) { cancelLiveSingle(); return; }
  stopSingleTimer();
  store.update((d) => { d.single = null; d.view = "home"; });
  toast("已停止");
}

export function exportSingle(): void {
  if (getState().single?.live) { void exportLiveSingle(); return; }
  closePop();
  const runId = nextRunId();
  store.update((d) => {
    const s = d.single!;
    s.phase = "exporting";
    s.stage = 0;
    s.sp = 0;
    s.runId = runId;
  });
  runProgress(60, () => 7, (d, s) => {
    s.phase = "done";
    const partial = isPartialResult(s);
    d.history.unshift({
      id: nextId("h"), k: "file", ext: s.ext, name: s.name, meta: s.pages.length + " 页", st: partial ? "part" : "done",
      stl: partial ? "部分结果" : "已导出", when: "刚刚", saved: s,
    });
  });
}

/* ── Undo ─────────────────────────────────────────────────────────────────── */

/** Remember the decisions as they are now; call inside the same update, before the change. */
export function pushUndo(d: Draft<AppState>): void {
  const s = d.single!;
  // Copy out of the draft: the snapshot must keep the values as they are before the change that follows.
  d.undo.push({ hits: plain(s.hits), pages: plain(s.pages), imgs: plain(s.imgs), catAct: plain(s.catAct) });
  if (d.undo.length > 50) d.undo.shift();
}

export function undoSingle(): void {
  const u = getState().undo.at(-1);
  if (!u) return;
  if (u.manual != null && getState().single?.live) {
    closePop();
    void withdrawLiveManual(u.manual).then((ok) => { if (ok) toast("已撤销"); });
    return;
  }
  store.update((d) => {
    d.undo.pop();
    Object.assign(d.single!, u);
  });
  closePop();
  toast("已撤销");
}

/** Applies a decision to the open document, remembering the previous state for undo. */
function decide(recipe: (s: Draft<Single>, d: Draft<AppState>) => void): void {
  store.update((d) => {
    if (!d.single) return;
    pushUndo(d);
    recipe(d.single, d);
  });
}

/* ── Hits ─────────────────────────────────────────────────────────────────── */

/** Where the popover of a hit attaches: the copy in the effect column when it was opened from there. */
export function hitElement(id: number): HTMLElement | null {
  const s = getState().single;
  const fx = s?.popFx && s.mode === "cmp" ? document.querySelector<HTMLElement>(`.paper.fx .hit[data-hit="${id}"]`) : null;
  return fx || document.getElementById("h" + id);
}

const openHitPopover = (id: number) => openPop(() => hitElement(id), { kind: "hit", hitId: id }, { role: "dialog", focus: false });

/** A hit was clicked in the document. `inEffectColumn` tells which side of the compare view it was on. */
export function clickHit(id: number, inEffectColumn: boolean): void {
  store.update((d) => {
    const s = d.single!;
    s.focus = id;
    s.popFx = inEffectColumn;
    if (s.hits[id]!.low) s.hits[id]!.seen = true;
  });
  openHitPopover(id);
}

let revealSeq = 0;
/** Focus a hit from the list or the keyboard: scroll to it and open its popover. */
export function focusHit(id: number): void {
  store.update((d) => {
    const s = d.single!;
    s.focus = id;
    s.popFx = false;
    if (s.hits[id]!.low) s.hits[id]!.seen = true;
    s.reveal = { id, seq: ++revealSeq };
  });
}

export function openRevealedHit(id: number): void {
  openHitPopover(id);
}

/** The document has taken up the reveal request. */
export function clearReveal(): void {
  if (getState().single?.reveal) store.update((d) => { d.single!.reveal = undefined; });
}

export function stepHit(d: number): void {
  const s = getState().single;
  if (!s) return;
  const n = s.hits.length;
  if (!n) return;
  const ordered = orderedHitIds(s);
  let i = ordered.indexOf(s.focus);
  i = i < 0 ? (d > 0 ? 0 : n - 1) : (i + d + n) % n;
  focusHit(ordered[i]!);
}

/** Jump to the next hit of a category (cycling). */
export function focusNextOfType(t: CategoryId): void {
  const s = getState().single!;
  const ids = orderedHitIds(s).filter((i) => s.hits[i]!.type === t);
  if (!ids.length) return;
  focusHit(ids[(ids.indexOf(s.focus) + 1) % ids.length]!);
}

export function clearFocus(): void {
  if (getState().single) store.update((d) => { d.single!.focus = -1; });
}

export function setHitState(state: "keep" | "redact"): void {
  const s = getState().single;
  const h = s?.hits[s.focus];
  if (!s || !h) return;
  let n = 1;
  decide((x) => {
    const targets = x.sameAll ? sameValueHits(x, x.hits[x.focus]!) : [x.hits[x.focus]!];
    targets.forEach((t) => { t.state = state; t.seen = true; });
    n = targets.length;
  });
  toast((state === "keep" ? "保留" : "涂抹") + (n > 1 ? " " + n + " 处" : ""), undoSingle);
}

export function toggleFocusedHit(): void {
  const s = getState().single;
  const h = s?.hits[s.focus];
  if (h) setHitState(h.state === "keep" ? "redact" : "keep");
}

export function setSameAll(on: boolean): void {
  store.update((d) => { d.single!.sameAll = on; });
}

/** Mask the selected text by hand. */
export function addManualRedaction(type: CategoryId, sel: ManualSelection): void {
  if (getState().single?.live) {
    void addLiveManual(type, sel).then((ok) => { if (ok) toast("已涂抹 · " + categoryName(getState().categories, type), undoSingle); });
    window.getSelection()?.removeAllRanges();
    closePop();
    return;
  }
  decide((s) => addManualHit(s, sel, type));
  window.getSelection()?.removeAllRanges();
  closePop();
  toast("已涂抹 · " + categoryName(getState().categories, type), undoSingle);
}

/* ── View ─────────────────────────────────────────────────────────────────── */

export function setViewMode(mode: ViewMode): void {
  store.update((d) => {
    if (!d.single || d.single.mode === mode) return;
    d.single.mode = mode;
    d.docMode = mode;
  });
}

export function setCurrentPage(page: number): void {
  if (getState().single?.page !== page) store.update((d) => { d.single!.page = page; });
}

export function setSinglePhase(phase: "result" | "manifest"): void {
  closeModal();
  store.update((d) => { d.single!.resultPhase = phase; });
}

/* ── Menus of the sidebar ─────────────────────────────────────────────────── */

export function openCategoryMenu(anchor: PopAnchor, t: CategoryId): void {
  const s = getState().single!;
  const name = categoryName(getState().categories, t);
  // the kernel writes one fixed placeholder form per category; the form cannot be chosen per document yet
  const items: MenuEntry[] = s.live
    ? [{ h: name }, { l: "全部保留", k: "keepAll" }]
    : [
      { h: name },
      { l: "涂抹", s: "■■■", k: "mask", on: treatmentOf(s, t) === "mask" },
      { l: "占位符", s: categoryPlaceholder(getState().categories, t), k: "ph", on: treatmentOf(s, t) === "ph" },
      "-",
      { l: "全部保留", k: "keepAll" },
    ];
  openMenu(anchor, items, (it) => {
    decide((x) => {
      if (it.k === "keepAll") x.hits.forEach((h) => { if (h.type === t) { h.state = "keep"; h.seen = true; } });
      else {
        x.catAct[t] = it.k as Treatment;
        if (x.mode === "orig" && it.k === "ph") x.mode = "fx";
      }
    });
    if (it.k === "keepAll") toast("保留全部" + name, undoSingle);
  }, { alignRight: true });
}

export function openAllImagesMenu(anchor: PopAnchor): void {
  const s = getState().single!;
  const verb = outputKind(s.ext).verb;
  const kept = s.imgs.filter((i) => i.act === "keep").length;
  openMenu(anchor, [
    { h: "全部 " + s.imgs.length + " 张图像" },
    { l: verb + "全部", k: "remove", on: !kept },
    { l: "保留全部", s: "部分结果", k: "keep", on: kept === s.imgs.length },
  ], (it) => decide((x) => x.imgs.forEach((i) => { i.act = it.k as ImageAction; })));
}

export function openImageMenu(anchor: PopAnchor, index: number): void {
  const s = getState().single!;
  const im = s.imgs[index]!;
  const verb = outputKind(s.ext).verb;
  openMenu(anchor, [
    { h: im.label + " · " + locAt(s.ext, im) },
    { l: verb, k: "remove", on: im.act !== "keep" },
    { l: "保留", s: "部分结果", k: "keep", on: im.act === "keep" },
  ], (it) => {
    if (getState().single!.imgs[index]!.act === it.k) return;
    decide((x) => { x.imgs[index]!.act = it.k as ImageAction; });
    toast((it.k === "keep" ? "保留" : verb) + " · " + im.label, undoSingle);
  });
}

export function openOutputMenu(anchor: PopAnchor): void {
  const s = getState().single!;
  if (s.live) {
    openMenu(anchor, [{ l: s.out || "默认位置", k: "cur", on: true }, { l: "选择…", ic: "folder", k: "pick" }], (it) => {
      if (it.k === "pick") void chooseOutputFolder((dir) => store.update((d) => { if (d.single) d.single.out = dir; }));
    });
    return;
  }
  openMenu(anchor, [
    { l: "桌面 / 已脱敏", k: "桌面 / 已脱敏", on: s.out === "桌面 / 已脱敏" },
    { l: "与原文件相同位置", k: "原文件旁", on: s.out === "原文件旁" },
    "-",
    { l: "选择…", ic: "folder", k: "文稿 / 脱敏" },
  ], (it) => store.update((d) => { d.single!.out = String(it.k); }));
}

export function openSingleSchemeMenu(anchor: PopAnchor): void {
  const s = getState();
  const single = s.single!;
  openMenu(anchor, [
    ...s.presets.map((p) => ({ l: p.name, s: p.ver, k: p.name, on: single.preset === p.name })),
    "-",
    { l: "管理方案", ic: "sliders", k: "_m" },
  ], (it) => {
    if (it.k === "_m") { openSubpage("presets", { presetTab: "scheme" }); return; }
    if (it.k === single.preset) return;
    const name = String(it.k);
    if (getState().undo.length) openModal({ type: "switchPreset", preset: name, go: () => switchPreset(name) });
    else switchPreset(name);
  });
}

/** Changing the scheme detects the document again; the user's decisions on it are dropped. */
export function switchPreset(name: string): void {
  closeModal();
  const s = getState().single!;
  if (!needModel(name, () => switchPreset(name), s.name + " · 切换方案")) return;
  const runId = nextRunId();
  store.update((d) => {
    const x = d.single!;
    x.preset = name;
    x.modelId = presetModelId(d.presets, name);
    Object.assign(x, buildDocument(), { catAct: {}, focus: -1, phase: "processing", stage: 0, sp: 0, runId });
    d.undo = [];
  });
  runSingleProcessing();
  toast("已切换 · 重新检测");
}

export function showKeyboardHelp(anchor: PopAnchor): void {
  openPop(anchor, { kind: "keys" }, { alignRight: true, focus: false });
}

/* ── Leaving ──────────────────────────────────────────────────────────────── */

export function revealOutput(): void {
  closeModal();
  const out = getState().single?.out ?? getState().batch?.out;
  toast(getState().single?.live || getState().batch?.live ? "已导出到 " + out : "已在访达中显示");
}

export function saveFile(name: string): void {
  closeModal();
  toast("已保存 " + name);
}

/** Done with a file that was opened from a batch: write the edits back and return to the batch. */
export function returnToBatch(): void {
  closePop();
  let left: ReturnType<typeof leaveBatchFile> = null;
  store.update((d) => { left = leaveBatchFile(d); d.view = "batch"; });
  reportLeftBatchFile(left, "batch");
}

/** From a batch file: open the review of that file's remaining exceptions. */
export function reviewBatchFileExceptions(): void {
  const fi = getState().single!.bfi!;
  closePop();
  let left: ReturnType<typeof leaveBatchFile> = null;
  store.update((d) => { left = leaveBatchFile(d); d.view = "batch"; });
  enterBatchReview(null, fi);
  if (left) toast("已保存修改");
}
