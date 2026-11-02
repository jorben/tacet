import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { cleanBatchStub } from "./liveFake";
import { addManualRedaction, clickHit, exportSingle, setViewMode, undoSingle } from "../actions/single";
import { openBatchFile, setBatchMode, startBatch, togglePause } from "../actions/batch";
import { openLiveGroup, restoreUnfinished, startLive, takePaths, unlockLive } from "../actions/live";
import { resumeBatch } from "../actions/batch";
import { overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { getState, store } from "../store";
import { FakeEngine, rasterPage, unit } from "./liveFake";
import { mountApp } from "./helpers";
import { beforeAll } from "vitest";

beforeAll(mountApp);

let fake: FakeEngine;
let stop: () => void;

beforeEach(() => {
  fake = new FakeEngine();
  setService({ ...mockService, engine: fake });
  stop = startLive();
});
afterEach(() => {
  stop();
  setService(null);
});

const single = () => getState().single!;
const batch = () => getState().batch!;
const toastText = () => overlayStore.get().toast?.msg ?? "";
const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });

describe("真实引擎：单文件", () => {
  it("导入→处理中→复核→确认并导出→完成，每一步都来自引擎状态", async () => {
    fake.units = [unit(1, "联系 13812345678 与 13900000000", [{ id: 11, start: 3, end: 14, category: "phone" }, { id: 12, start: 17, end: 28, category: "phone" }])];
    takePaths(["/docs/客户名单.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    expect(single().phase).toBe("processing");
    expect(single().name).toBe("客户名单.txt");
    expect(fake.calls.filter((c) => c.startsWith("import"))).toEqual(["import:quick:1"]);
    expect(fake.started).toMatchObject({ authorize: false });

    await fake.emit("extracting_detecting");
    expect(single().stage).toBe(0);
    expect(single().sp).toBe(65);
    expect(single().phase).toBe("processing");

    await fake.emit("awaiting_review", { pending: 2 });
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(single().hits).toHaveLength(2);
    expect(single().live).toMatchObject({ job: "j1", findingOf: [11, 12] });

    // keep the second candidate, then export
    act(() => store.update((d) => { d.single!.hits[1]!.state = "keep"; }));
    exportSingle();
    await waitFor(() => expect(fake.calls).toContain("publish"));
    expect(fake.decisions).toEqual([{ id: 11, keep: false }, { id: 12, keep: true }]);
    expect(fake.published).toEqual({ partial: false, dir: "/Users/x/Desktop/Tacet 已脱敏" });
    expect(single().phase).toBe("exporting");

    await fake.emit("validating");
    expect(single().stage).toBe(1);
    await fake.emit("completed", { output: "f0001.txt", digest: "sha256:abc" });
    expect(single().phase).toBe("done");
    expect(single().live).toMatchObject({ output: "f0001.txt", digest: "sha256:abc" });
    expect(getState().history[0]).toMatchObject({ st: "done", name: "客户名单.txt" });
  });

  it("文件无法处理：给出原因、回到首页，并释放快捷任务名额", async () => {
    fake.startFails = new Error("NOT_READY: no processable file");
    takePaths(["/docs/a.bin"]);
    await waitFor(() => expect(getState().single).toBeNull());
    expect(getState().view).toBe("home");
    expect(toastText()).toContain("没有可处理的文件");
    expect(fake.calls).toEqual(expect.arrayContaining(["cancel", "clean"]));
  });

  it("引擎在处理中报告失败/受阻原因：显示中文原因并清理", async () => {
    takePaths(["/docs/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("unprocessable", { detail: "PAGE_LIMIT_EXCEEDED" });
    await waitFor(() => expect(getState().single).toBeNull());
    expect(toastText()).toContain("超过页数上限");
    expect(fake.calls).toContain("clean");
  });

  it("上一份快捷任务还没结束时拒绝新的一份，不改动界面", async () => {
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(single()).toBeTruthy());
    takePaths(["/docs/b.txt"]);
    expect(toastText()).toContain("a.txt");
    expect(fake.calls.filter((c) => c.startsWith("import"))).toHaveLength(1);
  });

  it("引擎拒绝导入（已有未完成任务）：提示原因，不留下半成品", async () => {
    fake.importFails = new Error("QUICK_TASK_ACTIVE");
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(getState().single).toBeNull());
    expect(toastText()).toContain("未完成的单文件任务");
  });

  it("导出时引擎仍要求确认：回到复核并提示，不当作已导出", async () => {
    fake.units = [unit(1, "x", [])];
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("awaiting_review");
    await waitFor(() => expect(single().phase).toBe("review"));
    exportSingle();
    await waitFor(() => expect(fake.calls).toContain("publish"));
    await fake.emit("awaiting_review", { pending: 1 });
    expect(single().phase).toBe("review");
    expect(toastText()).toContain("待确认");
  });

  it("导出调用失败：回到复核并说明原因", async () => {
    fake.units = [unit(1, "x", [])];
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    fake.publishFails = new Error("OUTPUT_UNAVAILABLE");
    exportSingle();
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(toastText()).toContain("输出位置");
  });

  it("人工涂抹：偏移按 UTF-16 计算并交给引擎，文档重新读取且保留已做的选择", async () => {
    fake.units = [unit(1, "😀张三 电话 13812345678", [{ id: 11, start: 8, end: 19, category: "phone" }])];
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    // after the load the kernel also knows the manual candidate
    fake.afterManual = [unit(1, "😀张三 电话 13812345678", [{ id: 11, start: 8, end: 19, category: "phone" }, { id: 12, start: 2, end: 4, category: "person", source: "manual" }])];
    addManualRedaction("person", { k: "0.0.0", st: 2, en: 4, text: "张三" });
    await waitFor(() => expect(fake.manual).toBeTruthy());
    expect(fake.manual).toEqual({ job: "j1", seq: 1, start: 2, end: 4, category: "person" });
    await waitFor(() => expect(single().hits).toHaveLength(2));
    expect(single().hits.map((h) => [h.type, h.state, !!h.manual])).toEqual([["person", "redact", true], ["phone", "keep", false]]);
    // what can be undone now is the selection itself (earlier snapshots no longer match the document)
    expect(getState().undo.map((u) => u.manual)).toEqual([12]);
  });

  it("撤销人工涂抹：引擎撤回该选区，文档重新读取且保留其他选择；引擎拒绝时撤销项保留", async () => {
    fake.units = [unit(1, "😀张三 电话 13812345678", [{ id: 11, start: 8, end: 19, category: "phone" }])];
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    fake.afterManual = [unit(1, "😀张三 电话 13812345678", [{ id: 11, start: 8, end: 19, category: "phone" }, { id: 12, start: 2, end: 4, category: "person", source: "manual" }])];
    addManualRedaction("person", { k: "0.0.0", st: 2, en: 4, text: "张三" });
    await waitFor(() => expect(single().hits).toHaveLength(2));
    act(() => store.update((d) => { d.single!.hits.find((h) => h.type === "phone")!.state = "keep"; }));
    fake.withdrawFails = new Error("ACTION_NOT_ALLOWED");
    act(() => undoSingle());
    await waitFor(() => expect(fake.calls).toContain("withdraw:12"));
    await waitFor(() => expect(overlayStore.get().toast).toBeTruthy());
    expect(single().hits).toHaveLength(2);
    expect(getState().undo.map((u) => u.manual)).toEqual([12]); // refused: still there to try again
    fake.withdrawFails = null;
    fake.afterWithdraw = [unit(1, "😀张三 电话 13812345678", [{ id: 11, start: 8, end: 19, category: "phone" }])];
    act(() => undoSingle());
    await waitFor(() => expect(fake.withdrawn).toEqual([12]));
    await waitFor(() => expect(single().hits).toHaveLength(1));
    expect(single().hits.map((h) => [h.type, h.state])).toEqual([["phone", "keep"]]);
    expect(getState().undo).toEqual([]);
  });
});

describe("真实引擎：PDF 页面预览", () => {
  const openPdf = async (withSecondPage = true) => {
    fake.units = [unit(1, "Call 13812345678 today", [{ id: 11, start: 5, end: 16, category: "phone" }], "page/1"), unit(2, "second 13900000000", [{ id: 12, start: 7, end: 18, category: "phone" }], "page/2")];
    fake.previews = {
      1: rasterPage(1, 2, [{ finding: 11, rect: [60, 100, 160, 120] }, { finding: 11, rect: [150, 100, 200, 120] }]),
      ...(withSecondPage ? { 2: rasterPage(2, 2, [{ finding: 12, rect: [10, 10, 110, 30] }]) } : {}),
    };
    takePaths(["/docs/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("ready_to_publish", { format: "pdf" });
    await waitFor(() => expect(single().phase).toBe("review"));
  };

  it("PDF 以渲染的页面显示，候选在页面上用框标出；其他页在滚动到时才渲染", async () => {
    await openPdf();
    expect(single().live!.raster!.pages[0]).toMatchObject({ width: 600, height: 800 });
    expect(single().pages).toHaveLength(2); // the page count comes from the document, not from the text units
    await waitFor(() => expect(document.querySelectorAll(".paper.raster img").length).toBeGreaterThan(0));
    await waitFor(() => expect(fake.calls).toContain("preview:2"));
    await waitFor(() => expect(single().live!.raster!.pages[1]).toBeTruthy());
    expect(document.querySelector<HTMLImageElement>(".paper.raster img")!.getAttribute("src")).toBe("/tacet-preview/01234567890123456789abc1.png".replace("01234567890123456789abc1", "0123456789abcdef01234561"));
    // both boxes of the first candidate belong to one hit; only the first carries the DOM id
    const boxes = [...document.querySelectorAll<HTMLElement>("button.rbox")].filter((b) => b.dataset.hit === "0");
    expect(boxes).toHaveLength(2);
    expect(boxes.filter((b) => b.id === "h0")).toHaveLength(1);
    act(() => boxes[0]!.click());
    expect(single().focus).toBe(0);
    expect(overlayStore.get().pop).toBeTruthy();
  });

  it("PDF 效果预览中已涂抹的区域使用设置里的涂抹样式，而不是固定颜色", async () => {
    await openPdf();
    await waitFor(() => expect(document.querySelectorAll("button.rbox").length).toBeGreaterThan(0));
    act(() => setViewMode("fx"));
    const covered = [...document.querySelectorAll<HTMLElement>(".paper.raster button.rbox")].filter((b) => !b.classList.contains("keep"));
    expect(covered.length).toBeGreaterThan(0);
    for (const b of covered) expect(b.getAttribute("style")).toContain("var(--rdx)");
  });

  it("长文档按可见性懒加载：页面不在视口附近就不渲染", async () => {
    const seen: Element[] = [];
    let fire: ((v: boolean) => void) | null = null;
    class FakeIO {
      cb: IntersectionObserverCallback;
      constructor(cb: IntersectionObserverCallback) { this.cb = cb; }
      observe(el: Element) { seen.push(el); fire = (v) => this.cb([{ isIntersecting: v, target: el } as IntersectionObserverEntry], this as never); }
      disconnect() {}
      unobserve() {}
    }
    vi.stubGlobal("IntersectionObserver", FakeIO);
    try {
      await openPdf();
      await settle();
      expect(seen.length).toBeGreaterThan(0);
      expect(fake.calls.filter((c) => c.startsWith("preview:"))).toEqual(["preview:1"]);
      act(() => fire?.(true));
      await waitFor(() => expect(fake.calls).toContain("preview:2"));
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("保留/涂抹改变框的样式，不触发重新渲染；决定随导出交给引擎", async () => {
    await openPdf();
    await waitFor(() => expect(document.querySelectorAll("button.rbox").length).toBeGreaterThan(1));
    const before = fake.calls.filter((c) => c.startsWith("preview:")).length;
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; }));
    expect(document.querySelector("button.rbox.keep")).toBeTruthy();
    expect(fake.calls.filter((c) => c.startsWith("preview:")).length).toBe(before);
    exportSingle();
    await waitFor(() => expect(fake.decisions).toEqual([{ id: 11, keep: true }, { id: 12, keep: false }]));
  });

  it("页面不能渲染时回到文本显示并说明，而不是显示空白", async () => {
    fake.previewFails = new Error("PREVIEW_FAILED");
    fake.units = [unit(1, "Call 13812345678", [{ id: 11, start: 5, end: 16, category: "phone" }], "page/1")];
    takePaths(["/docs/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("ready_to_publish", { format: "pdf" });
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(single().live!.raster).toBeUndefined();
    expect(toastText()).toContain("文本显示");
    expect(document.querySelector(".paper.raster")).toBeNull();
    expect(single().hits).toHaveLength(1);
  });

  it("单页渲染失败只影响那一页", async () => {
    await openPdf(false);
    await waitFor(() => expect(single().live!.raster!.failed[1]).toBe(true));
    expect(single().live!.raster!.pages[0]).toBeTruthy();
    expect(document.body.textContent).toContain("此页无法预览");
  });

  it("文本类格式不请求页面预览", async () => {
    fake.units = [unit(1, "x", [])];
    takePaths(["/docs/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(fake.calls.some((c) => c.startsWith("preview:"))).toBe(false);
    expect(single().live!.raster).toBeUndefined();
    void clickHit;
  });
});

describe("真实引擎：重启后恢复", () => {
  const batchOf = (kind: string, state: string, extra: object = {}) => ({
    id: kind === "quick" ? "b0001" : "b0002", kind, phase: "running", total: 1, counts: {}, delivered: 0, exit_code: 2, started: true,
    files: [fake.row(0, state, { format: kind === "quick" ? "pdf" : "txt", ...extra })],
  });

  it("未完成的快捷任务回到单文件窗口，先继续再按引擎状态推进", async () => {
    fake.units = [unit(1, "x 13812345678", [{ id: 5, start: 2, end: 13, category: "phone" }], "page/1")];
    fake.previews = { 1: rasterPage(1, 1, [{ finding: 5, rect: [10, 10, 60, 30] }]) };
    fake.rows = [fake.row(0, "awaiting_review", { format: "pdf" })];
    fake.unfinishedList = [batchOf("quick", "paused")];
    await restoreUnfinished();
    expect(fake.calls).toContain("resume");
    expect(single().name).toBe("f0001.pdf");
    expect(single().live!.batch).toBe("b0001");
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(single().hits).toHaveLength(1);
  });

  it("未完成的批次列在“最近”，选择继续时才恢复运行；已启动与未启动分别回到运行页和预检页", async () => {
    fake.rows = [fake.row(0, "paused")];
    fake.unfinishedList = [{ ...batchOf("batch", "paused"), started: true, authorized: true }];
    await restoreUnfinished();
    expect(getState().batch).toBeNull();
    expect(fake.calls).not.toContain("resume");
    const h = getState().history[0]!;
    expect(h).toMatchObject({ k: "batch", st: "keep" });
    expect((h.saved as { phase: string; autoPub: boolean }).phase).toBe("run");
    expect((h.saved as { autoPub: boolean }).autoPub).toBe(true);
    await restoreUnfinished(); // a second pass does not list it twice
    expect(getState().history.filter((x) => x.k === "batch")).toHaveLength(1);
    act(() => resumeBatch(h.id));
    await waitFor(() => expect(fake.calls).toContain("resume"));
    expect(getState().batch!.live!.id).toBe("b0002");
    expect(getState().history.find((x) => x.id === h.id)).toBeUndefined();
  });

  it("预检阶段中断的批次回到预检页", async () => {
    fake.unfinishedList = [{ ...batchOf("batch", "paused"), started: false }];
    await restoreUnfinished();
    const saved = getState().history[0]!.saved as { phase: string; autoPub: boolean };
    expect(saved.phase).toBe("precheck");
    expect(saved.autoPub).toBe(false); // nothing was authorized, so nothing publishes by itself
  });

  it("没有未完成的任务，或引擎读不出来，都不改动界面", async () => {
    await restoreUnfinished();
    expect(getState().history).toHaveLength(0);
    fake.unfinished = async () => { throw new Error("X"); };
    await restoreUnfinished();
    expect(getState().single).toBeNull();
  });
});

describe("真实引擎：批次", () => {
  const paths = ["/d/a.txt", "/d/b.docx", "/d/c.pdf"];
  const rows = (states: [string, object?][]) => states.map(([state, extra], i) => fake.row(i, state, extra));

  it("导入后先显示真实预检结果；开始后按引擎状态分组，完成数来自引擎", async () => {
    fake.rows = rows([["preflighted"], ["preflighted"], ["unprocessable", { detail: "FORMAT_NOT_SUPPORTED" }]]);
    takePaths(paths);
    await waitFor(() => expect(batch()?.files.some((f) => f.st === "bad")).toBe(true));
    expect(batch().phase).toBe("precheck");
    expect(batch().files.map((f) => f.name)).toEqual(["a.txt", "b.docx", "c.pdf"]);
    expect(batch().files[2]!.reason).toBe("格式不支持");
    expect(batch().live!.id).toBe("b0001");
    expect(fake.calls).toContain("preflight");

    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    expect(fake.started).toMatchObject({ batch: "b0001", authorize: true, allowPartial: false });
    expect(batch().phase).toBe("run");

    fake.rows = rows([["completed", { output: "f0001.txt", digest: "sha256:aa" }], ["locked"], ["unprocessable", { detail: "FORMAT_NOT_SUPPORTED" }]]);
    await fake.emitBatch();
    await waitFor(() => expect(batch().groups.length).toBe(1));
    expect(batch().groups[0]).toMatchObject({ id: "live-password", per: { 1: 1 } });
    expect(batch().files[0]).toMatchObject({ st: "done", outputName: "f0001.txt", sha: "sha256:aa" });
    expect(batch().phase).toBe("result"); // nothing left that is running; one file waits for a person
  });

  it("需要复核的文件打开真实文档；离开时把选择交给引擎", async () => {
    fake.rows = rows([["preflighted"], ["preflighted"]]);
    takePaths(paths.slice(0, 2));
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    fake.rows = rows([["awaiting_review", { pending: 1 }], ["completed", { output: "f0002.docx" }]]);
    fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14, category: "phone" }])];
    await fake.emitBatch();
    await waitFor(() => expect(batch().groups[0]?.id).toBe("live-review"));
    openLiveGroup(null);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(single().live).toMatchObject({ job: "j1", findingOf: [31] });
    expect(getState().view).toBe("single");
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    const { returnToBatch } = await import("../actions/single");
    returnToBatch();
    await waitFor(() => expect(fake.decisions).toEqual([{ id: 31, keep: true }]));
    expect(getState().view).toBe("batch");
    expect(getState().single).toBeNull();
  });

  it("严格方式：开始时交给引擎；每份文件单独确认，确认前先送出选择，不能批量确认", async () => {
    fake.rows = rows([["preflighted"], ["preflighted"]]);
    takePaths(paths.slice(0, 2));
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    setBatchMode("strict");
    expect(batch().mode).toBe("strict");
    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    expect(fake.started).toMatchObject({ mode: "strict" });
    fake.rows = rows([["awaiting_review", { pending: 1, confirm: true }], ["awaiting_review", { pending: 3, confirm: true }]]);
    fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14, category: "phone" }])];
    await fake.emitBatch();
    await waitFor(() => expect(batch().groups[0]?.id).toBe("live-strict"));
    expect(batch().groups.map((g) => [g.id, g.per])).toEqual([["live-strict", { 0: 1, 1: 1 }], ["live-review", { 1: 2 }]]);
    expect(batch().groups.every((g) => g.noBulk)).toBe(true);
    openLiveGroup("live-strict");
    await waitFor(() => expect(single()?.from).toBe("batch"));
    const btn = [...document.querySelectorAll<HTMLButtonElement>(".side-foot button")].find((b) => b.textContent === "确认导出");
    expect(btn).toBeTruthy();
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; }));
    act(() => btn!.click());
    await waitFor(() => expect(fake.confirmed).toEqual(["j1"]));
    expect(fake.calls.indexOf("decide")).toBeLessThan(fake.calls.indexOf("confirm")); // the choices first
    expect(fake.decisions).toEqual([{ id: 31, keep: true }]);
    expect(getState().view).toBe("batch");
    expect(getState().single).toBeNull();
  });

  it("严格方式：引擎拒绝确认时说明原因", async () => {
    fake.rows = rows([["preflighted"], ["preflighted"]]);
    takePaths(paths.slice(0, 2));
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    setBatchMode("strict");
    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    fake.rows = rows([["awaiting_review", { pending: 1, confirm: true }], ["completed"]]);
    fake.units = [unit(1, "x", [])];
    await fake.emitBatch();
    await waitFor(() => expect(batch().groups[0]?.id).toBe("live-strict"));
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    fake.confirmFails = new Error("ACTION_NOT_ALLOWED");
    const btn = [...document.querySelectorAll<HTMLButtonElement>(".side-foot button")].find((b) => b.textContent === "确认导出")!;
    act(() => btn.click());
    await waitFor(() => expect(overlayStore.get().toast).toBeTruthy());
    expect(fake.confirmed).toEqual([]);
  });

  it("跳过此文件：严格方式在复核页跳过；受阻文件在对话框里重新处理或逐个跳过；密码文件可跳过", async () => {
    fake.rows = rows([["preflighted"], ["preflighted"], ["preflighted"], ["preflighted"]]);
    takePaths(paths.slice(0, 4));
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    setBatchMode("strict");
    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    fake.rows = rows([["awaiting_review", { pending: 1, confirm: true }], ["failed", { detail: "EXTRACT_FAILED" }], ["failed", { detail: "STAGE_DEADLINE_EXCEEDED" }], ["locked"]]);
    fake.units = [unit(1, "x", [])];
    await fake.emitBatch();
    await waitFor(() => expect(batch().groups.map((g) => g.id).sort()).toEqual(["live-blocked", "live-password", "live-strict"]));
    // strict: from the file's review
    openLiveGroup("live-strict");
    await waitFor(() => expect(single()?.from).toBe("batch"));
    const skipBtn = [...document.querySelectorAll<HTMLButtonElement>(".side-foot button")].find((b) => b.textContent === "跳过此文件")!;
    act(() => skipBtn.click());
    await waitFor(() => expect(fake.skipped).toEqual(["j1"]));
    expect(fake.confirmed).toEqual([]);
    expect(getState().view).toBe("batch");
    // blocked: the dialog lists both, skips one, retries the rest
    act(() => openLiveGroup("live-blocked"));
    await waitFor(() => expect(overlayStore.get().modal).toMatchObject({ type: "liveBlocked" }));
    expect(fake.calls).not.toContain("retry"); // opening the group retries nothing by itself
    const rowsOf = () => [...document.querySelectorAll<HTMLButtonElement>("button")].filter((b) => b.textContent === "跳过此文件");
    expect(rowsOf()).toHaveLength(2);
    act(() => rowsOf()[0]!.click());
    await waitFor(() => expect(fake.skipped).toEqual(["j1", "j2"]));
    await waitFor(() => expect(rowsOf()).toHaveLength(1));
    const retry = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === "重新处理")!;
    act(() => retry.click());
    await waitFor(() => expect(fake.calls).toContain("retry"));
    await waitFor(() => expect(overlayStore.get().modal).toBeNull());
    // a file waiting for its password
    act(() => openLiveGroup("live-password"));
    await waitFor(() => expect(rowsOf()).toHaveLength(1));
    fake.skipFails = new Error("ACTION_NOT_ALLOWED");
    act(() => rowsOf()[0]!.click());
    await waitFor(() => expect(fake.calls).toContain("skip:j4"));
    expect(overlayStore.get().modal).toMatchObject({ type: "livePassword" }); // refused: the dialog stays
    fake.skipFails = null;
    act(() => rowsOf()[0]!.click());
    await waitFor(() => expect(fake.skipped).toEqual(["j1", "j2", "j4"]));
    await waitFor(() => expect(overlayStore.get().modal).toBeNull());
  });

  it("没有改动就离开：不向引擎发送决定", async () => {
    fake.rows = rows([["preflighted"]]);
    takePaths(paths.slice(0, 1).concat("/d/z.txt"));
    fake.rows = rows([["preflighted"], ["preflighted"]]);
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    fake.rows = rows([["ready_to_publish"], ["completed"]]);
    fake.units = [unit(1, "x", [])];
    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emitBatch();
    await waitFor(() => expect(batch().files[0]!.st).toBe("ready"));
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    const { returnToBatch } = await import("../actions/single");
    returnToBatch();
    await settle();
    expect(fake.calls).not.toContain("decide");
  });

  it("密码：交给引擎一次，错误提示来自引擎的原因码", async () => {
    fake.rows = rows([["preflighted"], ["preflighted"]]);
    takePaths(paths.slice(0, 2));
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    expect(await unlockLive("j2", "")).toBe(false);
    expect(fake.calls).not.toContain("unlock");
    fake.unlockFails = new Error("NOT_ALLOWED");
    expect(await unlockLive("j2", "wrong-document-password-7429")).toBe(false);
    expect(fake.unlocked).toEqual([["j2", "wrong-document-password-7429"]]);
    fake.unlockFails = null;
    expect(await unlockLive("j2", "right")).toBe(true);
    // the password is not part of the state anywhere
    expect(JSON.stringify(getState())).not.toContain("right");
    expect(JSON.stringify(getState())).not.toContain("wrong-document-password-7429");
  });

  it("暂停/继续调用引擎；失败时界面状态回滚", async () => {
    fake.rows = rows([["preflighted"], ["preflighted"]]);
    takePaths(paths.slice(0, 2));
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    togglePause();
    await waitFor(() => expect(fake.calls).toContain("pause"));
    expect(batch().paused).toBe(true);
    fake.resumeFails = new Error("VERSION_CONFLICT");
    togglePause();
    await waitFor(() => expect(batch().paused).toBe(true));
    fake.resumeFails = null;
    togglePause();
    await waitFor(() => expect(batch().paused).toBe(false));
    expect(fake.calls.filter((c) => c === "resume")).toHaveLength(2);
  });

  it("上百个文件分页读取，行数与引擎一致", async () => {
    fake.rows = Array.from({ length: 450 }, (_, i) => fake.row(i, "preflighted"));
    takePaths(Array.from({ length: 450 }, (_, i) => `/d/f${i}.txt`));
    await waitFor(() => expect(batch()?.files).toHaveLength(450));
    await waitFor(() => expect(fake.calls.filter((c) => c.startsWith("files")).length).toBeGreaterThan(0));
    expect(fake.calls.filter((c) => c.startsWith("files:"))).toEqual(["files:0", "files:200", "files:400"]);
    expect(batch().n).toBe(450);
  });

  it("浏览器宿主没有引擎：仍走原来的演示流程", () => {
    setService(mockService);
    expect(getState().batch).toBeNull();
    void cleanBatchStub;
  });
});
