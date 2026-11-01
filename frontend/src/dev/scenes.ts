// Fixtures for the scene navigator and for tests: each scene puts the application into a known state.
// Development and test builds only (see `__SCENES__`).
import { requestClose } from "../actions/window";
import { openGroups, groupMembers } from "../domain/batch";
import { DEMO_FILE_KINDS, GIVEN_NAMES, SURNAMES } from "../domain/catalog";
import { pick, reseed } from "../domain/random";
import { resetOverlay, dismissToast } from "../overlay/store";
import { enterBatchReview } from "../actions/review";
import { newBatch, resolveException, startBatch, tickBatch } from "../actions/batch";
import { takeFiles } from "../actions/home";
import { resetModels, downloadModel, prepareModel } from "../actions/models";
import { exportSingle, newSingle } from "../actions/single";
import { stopAllTimers } from "../actions/timers";
import { getState, store } from "../store";
import type { HistoryEntry } from "../domain/types";

export function demoNames(n: number): string[] {
  reseed(99);
  return Array.from({ length: n }, (_, i) => {
    const [kind, ext] = pick(DEMO_FILE_KINDS);
    return kind.replace("{n}", pick(SURNAMES) + pick(GIVEN_NAMES)).replace("{k}", String(100 + i)) + "." + (i === 37 ? "pages" : ext);
  });
}

/** The prototype's preloaded “recent” list. Production starts empty; scenes seed it to keep parity with the prototype. */
export const demoHistory = (): HistoryEntry[] => [
  { id: "demo-1", k: "batch", name: "季度客户材料", meta: "48 份", st: "done", stl: "已导出 48", when: "3 天前" },
  { id: "demo-2", k: "file", name: "述职报告.docx", ext: "docx", meta: "12 页", st: "done", stl: "已导出", when: "昨天" },
  { id: "demo-3", k: "batch", name: "供应商合同", meta: "26 份", st: "keep", stl: "已保存 · 6 天后清理", when: "上周" },
];

let seeded = false;
/** Forget that the demo history was added (tests that reset the store call this too). */
export const resetScenes = (): void => { seeded = false; };

export const SCENE_GROUPS: [string, [string, string][]][] = [
  ["入口", [["首页", "home"], ["首页 · 首次使用", "fresh"]]],
  ["模型准备", [["下载中", "model-download"], ["下载已暂停", "model-paused"], ["断网 / 下载中断", "model-offline"], ["空间不足", "model-disk"], ["校验失败", "model-integrity"], ["加载与试运行", "model-loading"], ["启动失败", "model-load"], ["已选文件 · 等待模型", "model-wait"], ["批量 · 模型未就绪", "model-batch"], ["插件 · 模型待下载", "model-plugins"], ["插件 · 两个模型", "model-catalog"]]],
  ["单份", [["检测中", "s1"], ["复核", "s2"], ["复核 · DOCX", "s5"], ["左右对比", "s4"], ["完成", "s3"]]],
  ["批量", [["预检", "b1"], ["处理中", "b2"], ["人工复核", "b5"], ["完成", "b3"], ["无人值守 · 完成", "b6"], ["导出清单", "b4"]]],
  ["其他", [["方案", "presets"], ["插件", "plugins"], ["设置", "settings"], ["关闭窗口", "close"]]],
];

function ticks(n: number): void {
  for (let i = 0; i < n; i++) tickBatch();
}

/** Puts the application into the state of scene `key`. */
export function scene(key: string): void {
  resetOverlay();
  if (key === "blank") {
    stopAllTimers();
    resetModels("ready");
    store.update((d) => { d.history = []; d.single = null; d.batch = null; d.navStack = []; d.view = "home"; });
    return;
  }
  if (!seeded) {
    seeded = true;
    store.update((d) => { d.history = demoHistory().concat(d.history); });
  }
  stopAllTimers();
  resetModels();
  store.update((d) => { d.behind = null; d.navStack = []; d.history = d.history.filter((h) => !h.park && !h.saved); });

  if (["s1", "s2", "s3", "s4", "s5"].includes(key)) {
    newSingle(key === "s5" ? "劳动合同.docx" : "劳动合同.pdf", 1887436, key === "s1" ? {} : { phase: "review", mode: key === "s4" ? "cmp" : "orig" });
    if (key === "s3") {
      store.update((d) => { d.single!.hits[13]!.state = "keep"; });
      exportSingle();
    }
    return;
  }
  if (key.startsWith("b")) {
    store.update((d) => { d.batch = null; });
    newBatch(demoNames(214), { title: "客户材料 · 10 月" });
    if (key === "b1") return;
    if (key === "b6") {
      store.update((d) => { d.batch!.mode = "auto"; });
      startBatch();
      for (let i = 0; i < 1500 && getState().batch!.phase !== "result"; i++) tickBatch();
      return;
    }
    startBatch();
    if (key === "b5") { ticks(60); enterBatchReview(null); return; }
    if (key === "b2") { ticks(34); return; }
    for (let round = 0; round < 3; round++) {
      ticks(120);
      const b = getState().batch!;
      openGroups(b).forEach((g) => { if (groupMembers(b, g).length) resolveException(g.id, g.acts[0]!.k); });
    }
    ticks(60);
    dismissToast();
    if (key === "b4") store.update((d) => { d.batch!.phase = "manifest"; });
    return;
  }
  if (key.startsWith("model-")) {
    const what = key.slice(6);
    store.update((d) => { d.history = []; d.single = null; d.batch = null; d.homePreset = "通用办公脱敏"; d.view = "home"; });
    resetModels("missing");
    if (what === "plugins") { store.update((d) => { d.view = "plugins"; }); return; }
    if (what === "catalog") {
      // the live catalog's shape: the default model downloaded, an optional larger one not yet
      store.update((d) => {
        d.view = "plugins";
        d.models.splice(1);
        Object.assign(d.models[0]!, {
          status: "ready", progress: 100,
          runtime: {
            dir: "/Users/jorben/Library/Application Support/Tacet/models/installed/astrlink-guard_0.1.3_0a070d3113615e33",
            backend: "CPUExecutionProvider", device: "cpu", repository: "QuantumNous/astrlink-guard", accelerable: true, accelerated: false,
            accel: { id: "astrlink-guard-fp16", variant: "FP16", status: "missing", progress: 0, error: null, weightMiB: 72 },
          },
        });
        d.models.push({
          id: "pf", name: "OpenAI Privacy Filter", version: "7ffa9a0", variant: "INT8", languages: "英文为主", weightMiB: 1569, recommended: false,
          confRec: 0.5, confMin: 0.2, status: "missing", progress: 0, error: null, eligible: true, minMemoryGiB: 16,
          runtime: { dir: "", backend: "CPUExecutionProvider", device: "cpu", repository: "openai/privacy-filter", accelerable: false, accelerated: false },
        });
      });
      return;
    }
    if (what === "batch") { newBatch(demoNames(12), { title: "待处理材料" }); return; }
    if (what === "download") { store.update((d) => { d.models[0]!.progress = 35; }); downloadModel(); return; }
    if (what === "paused") store.update((d) => { Object.assign(d.models[0]!, { status: "paused", progress: 45 }); });
    if (["offline", "disk", "integrity", "load"].includes(what)) {
      store.update((d) => { Object.assign(d.models[0]!, { status: "error", error: what, progress: what === "offline" ? 45 : what === "disk" ? 20 : 100 }); });
    }
    if (what === "loading") { prepareModel(); return; }
    if (what === "wait") takeFiles([{ name: "劳动合同.pdf", size: 1887436 }]);
    return;
  }
  if (key === "fresh") {
    resetModels("missing");
    store.update((d) => { d.homePreset = "通用办公脱敏"; d.history = []; d.single = null; d.batch = null; d.view = "home"; });
    return;
  }
  if (key === "close") { requestClose(); return; }
  store.update((d) => { d.view = key as typeof d.view; });
}
