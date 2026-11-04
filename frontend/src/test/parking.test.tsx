// A task waiting for the person gives way to the next one and stays under “最近”; nothing asks first (FR-28).
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { cancelBatch, openBatchFile, resumeBatch, startBatch, togglePause } from "../actions/batch";
import { discardSavedTask, needSlot, openHistoryEntry, openHistoryResult, resumeSavedBatch } from "../actions/home";
import { startReview } from "../actions/review";
import { restoreUnfinished, startLive, stopLiveBatch, takePaths } from "../actions/live";
import { cleanWorkspace } from "../actions/settings";
import { exportSingle, returnToBatch, reviewBatchFileExceptions } from "../actions/single";
import { requestQuit, saveAndQuit, stopAndQuit } from "../actions/window";
import { createSingle } from "../domain/single";
import type { Batch, Single } from "../domain/types";
import { closeModal, openModal, overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { getState, resetStore, store } from "../store";
import { FakeEngine, rasterPage, unit } from "./liveFake";
import { buttons, mountApp, win } from "./helpers";

beforeAll(mountApp);

let fake: FakeEngine;
let stop: () => void;

beforeEach(() => {
  act(() => { resetStore(); closeModal(); });
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
const history = () => getState().history;
const toastText = () => overlayStore.get().toast?.msg ?? "";
const imports = () => fake.calls.filter((c) => c.startsWith("import"));
const entryOf = (id: string) => history().find((h) => (h.saved as { live?: { id?: string; batch?: string } } | undefined)?.live?.[(h.k === "batch" ? "id" : "batch")] === id);
const rowsOf = (...states: [string, object?][]) => states.map(([state, extra], i) => fake.row(i, state, extra));

/** A single task, in review. */
async function reviewing(path = "/d/a.txt") {
  fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
  takePaths([path]);
  await waitFor(() => expect(fake.calls).toContain("start"));
  await fake.emit("awaiting_review", { pending: 1 });
  await waitFor(() => expect(single().phase).toBe("review"));
}

/** A batch of two started; its files are then in the given states. */
async function runningBatch(...states: [string, object?][]) {
  fake.rows = rowsOf(["preflighted"], ["preflighted"]);
  takePaths(["/d/a.txt", "/d/b.txt"]);
  await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
  await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
  startBatch();
  await waitFor(() => expect(fake.calls).toContain("start"));
  await waitFor(() => expect(batch().phase).toBe("run"));
  fake.rows = rowsOf(...states);
  await fake.emitBatch();
}

describe("单文件：等人的任务给新任务让路", () => {
  it("待确认时拖入另一份：不弹窗，直接开始新任务；旧任务和它的选择进入“最近”", async () => {
    await reviewing();
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    fake.calls = [];
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    expect(overlayStore.get().modal).toBeNull();
    expect(imports()).toEqual(["import:quick:1"]);
    expect(fake.parkAsked).toEqual([true, true]);
    expect(single().live!.batch).toBe("b0002");
    expect(toastText()).toContain("已保存到“最近”");
    const h = entryOf("b0001")!;
    expect(h).toMatchObject({ k: "file", name: "a.txt", st: "warn", stl: "待确认", park: true });
    expect((h.saved as Single).hits[0]!.state).toBe("keep");
    expect(h.undo).toHaveLength(1);
    await waitFor(() => expect(fake.decisions).toEqual([{ id: 11, keep: true }])); // kept by the kernel: “最近” survives a restart
  });

  it("还在处理时拖入另一份：只提示等待，不调用引擎", async () => {
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    fake.calls = [];
    takePaths(["/d/b.txt"]);
    expect(toastText()).toContain("正在处理");
    expect(imports()).toEqual([]);
    expect(single().name).toBe("a.txt");
  });

  it("导出途中拖入另一份：同样只提示等待", async () => {
    await reviewing();
    exportSingle();
    await waitFor(() => expect(single().phase).toBe("exporting"));
    fake.calls = [];
    takePaths(["/d/b.txt"]);
    expect(imports()).toEqual([]);
    expect(toastText()).toContain("正在处理");
  });

  it("引擎拒绝导入：屏幕上的任务不被覆盖，选择都在", async () => {
    await reviewing();
    const runId = single().runId;
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; }));
    fake.importFails = new Error("QUICK_TASK_ACTIVE");
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(toastText()).toContain("未完成的单文件任务"));
    expect(single().runId).toBe(runId);
    expect(single().hits[0]!.state).toBe("keep");
    expect(getState().view).toBe("single");
    expect(history()).toHaveLength(0);
  });

  it("重新打开：本地选择还在，从引擎读最新状态，可以导出到它自己的批次", async () => {
    await reviewing();
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    openHistoryEntry(entryOf("b0001")!.id);
    expect(overlayStore.get().modal).toMatchObject({ type: "resumeBatch" });
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(fake.resumed).toEqual(["b0001"]));
    expect(single().name).toBe("a.txt");
    expect(single().hits[0]!.state).toBe("keep");
    expect(getState().undo).toHaveLength(1);
    // the one that was on screen is the one parked now
    expect(entryOf("b0002")).toMatchObject({ name: "b.txt", park: true });
    exportSingle();
    await waitFor(() => expect(fake.calls).toContain("publish"));
    expect(fake.publishedBatch).toBe("b0001");
    expect(fake.decisions).toEqual([{ id: 11, keep: true }]);
  });

  it("重新打开 PDF：之前渲染的页面令牌不再使用，页面重新向引擎要", async () => {
    fake.units = [unit(1, "Call 13812345678", [{ id: 11, start: 5, end: 16 }], "page/1")];
    fake.previews = { 1: rasterPage(1, 1, []) };
    takePaths(["/d/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("ready_to_publish", { format: "pdf" });
    await waitFor(() => expect(single().live?.raster?.pages[0]).toBeTruthy());
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    fake.calls = [];
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(single().name).toBe("a.pdf"));
    await waitFor(() => expect(fake.calls).toContain("preview:1"));
  });

  it("屏幕上的单文件还在处理时继续另一份：只提示等待，屏幕和列表都不变", async () => {
    await reviewing();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    expect(single().phase).toBe("processing");
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    expect(toastText()).toContain("正在处理");
    expect(single().name).toBe("b.txt");
    expect(fake.resumed).toEqual([]);
    expect(entryOf("b0001")).toBeTruthy();
  });

  it("已导出的单文件被新任务取代：它已经在“最近”里，不再列一次", async () => {
    await reviewing();
    exportSingle();
    await waitFor(() => expect(single().phase).toBe("exporting"));
    await fake.emit("completed", { output: "f0001.txt" });
    await waitFor(() => expect(single().phase).toBe("done"));
    expect(history()).toHaveLength(1);
    fake.rows = rowsOf(["completed"]);
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    expect(history()).toHaveLength(1);
    expect(history()[0]).toMatchObject({ st: "done", name: "a.txt" });
    expect(toastText()).not.toContain("已保存到");
  });

  it("停放的任务收到事件：不影响屏幕上的任务", async () => {
    await reviewing();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("extracting_detecting");
    const before = { name: single().name, stage: single().stage, sp: single().sp, phase: single().phase };
    await fake.emitFor("b0001", [fake.row(0, "failed", { detail: "EXTRACT_FAILED" })]);
    expect({ name: single().name, stage: single().stage, sp: single().sp, phase: single().phase }).toEqual(before);
    expect(fake.calls).not.toContain("cancel");
    expect(entryOf("b0001")).toBeTruthy();
  });

  it("批次的文件正打开时拖入单文件：先把选择交给引擎再关掉，背后的单文件任务进入“最近”", async () => {
    await reviewing();
    const behind = single().runId;
    await runningBatchBeside();
    fake.units = [unit(1, "x", [{ id: 31, start: 0, end: 1 }])];
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(getState().behind?.single.runId).toBe(behind);
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    takePaths(["/d/c.txt"]);
    await waitFor(() => expect(single().name).toBe("c.txt"));
    expect(getState().behind).toBeNull();
    await waitFor(() => expect(fake.decisions).toEqual([{ id: 31, keep: true }]));
    expect(history().some((h) => h.name === "a.txt" && h.park)).toBe(true);
  });
});

/** Beside a quick task in review: a batch with a file to review. */
async function runningBatchBeside() {
  fake.nextRows = rowsOf(["preflighted"], ["preflighted"]);
  takePaths(["/d/x.txt", "/d/y.txt"]);
  await waitFor(() => expect(batch()?.live?.id).toBe("b0002"));
  await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
  startBatch();
  await waitFor(() => expect(fake.begunCount()).toBe(2));
  await waitFor(() => expect(batch().phase).toBe("run"));
  await fake.emitFor("b0002", rowsOf(["awaiting_review", { pending: 1 }], ["completed"]));
  await waitFor(() => expect(batch().groups[0]?.id).toBe("live-review"));
}

describe("批次：只剩等人的文件时给新批次让路", () => {
  it("只剩待处理文件时拖入新批次：原批次进入“最近”，新批次开始（缺陷 1）", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    fake.calls = [];
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect(overlayStore.get().modal).toBeNull();
    expect(imports()).toEqual(["import:batch:2"]);
    expect(toastText()).toContain("已保存到“最近”");
    const h = entryOf("b0001")!;
    expect(h).toMatchObject({ k: "batch", st: "keep", park: true });
    expect(h.stl).toContain("待你处理");
    expect((h.saved as Batch).files).toHaveLength(2);
  });

  it("暂停并保存后开始新批次，再恢复旧批次：都成功（缺陷 2）", async () => {
    await runningBatch(["extracting_detecting"], ["queued"]);
    await stopLiveBatch(true);
    expect(getState().batch).toBeNull();
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch()?.live?.id).toBe("b0002"));
    expect(toastText()).not.toContain("未完成");
    // the new one has not started: the old one cannot take the screen from it
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    expect(fake.resumed).toEqual([]);
    expect(toastText()).toContain("还未开始");
    await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
    fake.calls = [];
    act(() => cancelBatch());
    await waitFor(() => expect(fake.cancelled).toContain("b0002"));
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(fake.resumed).toEqual(["b0001"]));
    expect(batch().live!.id).toBe("b0001");
    expect(batch().paused).toBe(false);
  });

  it("已暂停的批次（未离开页面）拖入新批次：也让路", async () => {
    await runningBatch(["extracting_detecting"], ["queued"]);
    togglePause();
    await waitFor(() => expect(fake.calls).toContain("pause"));
    await waitFor(() => expect(batch().paused).toBe(true));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect(entryOf("b0001")).toMatchObject({ st: "keep", park: true });
  });

  it("还有文件在处理时拖入新批次：提示等待，屏幕不变，也不调用引擎", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["extracting_detecting"]);
    fake.calls = [];
    takePaths(["/d/c.txt", "/d/d.txt"]);
    expect(toastText()).toContain("正在处理");
    expect(imports()).toEqual([]);
    expect(batch().live!.id).toBe("b0001");
  });

  it("文件待导出且已授权（会自己发布）：算处理中；未授权才算等人", async () => {
    await runningBatch(["ready_to_publish"], ["completed"]);
    expect(batch().autoPub).toBe(true);
    fake.calls = [];
    takePaths(["/d/c.txt", "/d/d.txt"]);
    expect(imports()).toEqual([]);
    expect(toastText()).toContain("正在处理");
    act(() => store.update((d) => { d.batch!.autoPub = false; }));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(imports()).toEqual(["import:batch:2"]));
  });

  it("从“最近”继续另一个批次：屏幕上只等人的批次让路，它也进入“最近”", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    await fake.emitFor("b0002", rowsOf(["preflighted"], ["preflighted"]));
    await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
    startBatch();
    await waitFor(() => expect(batch().phase).toBe("run"));
    await fake.emitFor("b0002", rowsOf(["awaiting_review", { pending: 1 }], ["completed"]));
    await waitFor(() => expect(batch().phase).toBe("result"));
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(batch().live!.id).toBe("b0001"));
    expect(fake.resumed).toEqual(["b0001"]);
    expect(overlayStore.get().modal).toBeNull();
    expect(entryOf("b0002")).toMatchObject({ st: "keep", park: true });
  });

  it("批次的文件正打开时拖入新批次：先把选择交给引擎再关掉，屏幕上是新批次", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    fake.units = [unit(1, "x", [{ id: 31, start: 0, end: 1 }])];
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect(getState().single).toBeNull();
    expect(getState().view).toBe("batch");
    await waitFor(() => expect(fake.decisions).toEqual([{ id: 31, keep: true }]));
  });

  it("屏幕上的批次还在处理时继续另一个：只提示等待，屏幕和列表都不变", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    await fake.emitFor("b0002", rowsOf(["preflighted"], ["preflighted"]));
    await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
    startBatch();
    await waitFor(() => expect(batch().phase).toBe("run"));
    await fake.emitFor("b0002", rowsOf(["extracting_detecting"], ["queued"]));
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    expect(toastText()).toContain("正在处理");
    expect(batch().live!.id).toBe("b0002");
    expect(fake.resumed).toEqual([]);
    expect(entryOf("b0001")).toBeTruthy();
  });

  it("未开始的草稿：引擎拒绝，提示原因，屏幕不变", async () => {
    fake.rows = rowsOf(["preflighted"], ["preflighted"]);
    takePaths(["/d/a.txt", "/d/b.txt"]);
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(toastText()).toContain("尚未开始"));
    expect(batch().live!.id).toBe("b0001");
    expect(batch().phase).toBe("precheck");
    expect(history()).toHaveLength(0);
  });

  it("文件全被拒绝的草稿：引擎放行时不留在“最近”，它的数据被清理", async () => {
    fake.rows = rowsOf(["unprocessable"], ["unprocessable"]);
    takePaths(["/d/a.txt", "/d/b.txt"]);
    await waitFor(() => expect(batch()?.files[0]?.st).toBe("bad"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect(history()).toHaveLength(0);
    expect(fake.cleaned).toEqual(["b0001"]);
    expect(toastText()).not.toContain("已保存到");
  });

  it("全部交付的批次：作为结果留在“最近”，不算未完成", async () => {
    await runningBatch(["completed"], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect(entryOf("b0001")).toMatchObject({ st: "done" });
    expect(entryOf("b0001")!.park).toBeUndefined();
  });

  it("继续过的停放批次还在跑：内核不再为它占名额，界面也不让新批次把它挤走", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    await waitFor(() => expect(batch().files[0]?.st).toBeTruthy());
    act(() => cancelBatch());
    await waitFor(() => expect(fake.cancelled).toContain("b0002"));
    act(() => resumeBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(batch().live!.id).toBe("b0001"));
    await fake.emitFor("b0001", rowsOf(["resolving"], ["completed"]));
    await waitFor(() => expect(batch().phase).toBe("run"));
    fake.calls = [];
    takePaths(["/d/e.txt", "/d/f.txt"]);
    expect(imports()).toEqual([]);
    expect(toastText()).toContain("正在处理");
  });
});

describe("演示流程（没有内核）", () => {
  it("批次文件打开时要让路：让路的是它背后的单文件任务，打开的文件先关上", () => {
    setService(mockService);
    const base = { mode: "orig" as const, preset: "办公外发", modelId: null };
    act(() => store.update((d) => {
      d.single = createSingle("批次里的.txt", "txt", undefined, base, { phase: "review", from: "batch", bfi: 0 });
      d.behind = { single: createSingle("背后的.txt", "txt", undefined, base, { phase: "review" }), undo: [] };
    }));
    let went = 0;
    act(() => needSlot("single", () => { went++; }));
    expect(went).toBe(1);
    expect(overlayStore.get().modal).toBeNull();
    expect(history().map((h) => h.name)).toEqual(["背后的.txt"]);
    expect(getState().single).toBeNull();
    expect(getState().behind).toBeNull();
  });
});

describe("重启之后", () => {
  const q = (id: string, parked: boolean) => ({ id, kind: "quick", phase: "running", total: 1, counts: {}, delivered: 0, exit_code: 2, started: true, parked, files: [fake.row(0, "paused", { format: "pdf" })] });
  const b = (id: string, parked: boolean) => ({ id, kind: "batch", phase: "running", total: 1, counts: {}, delivered: 0, exit_code: 2, started: true, parked, files: [fake.row(0, "paused")] });

  it("多个停放的单文件全部列出，都不恢复运行；占名额的那份回到窗口", async () => {
    fake.rows = [fake.row(0, "awaiting_review", { format: "pdf" })];
    fake.unfinishedList = [q("b0001", true), q("b0002", true), q("b0003", false), b("b0004", true), b("b0005", false)];
    await restoreUnfinished();
    expect(single().live!.batch).toBe("b0003");
    expect(fake.resumed).toEqual(["b0003"]);
    for (const id of ["b0001", "b0002"]) expect(entryOf(id)).toMatchObject({ k: "file", park: true, stl: "待确认", meta: id });
    for (const id of ["b0004", "b0005"]) expect(entryOf(id)).toMatchObject({ k: "batch", park: true, st: "keep" });
    expect(history()).toHaveLength(4);
    // another pass finds them all tracked
    fake.calls = [];
    await restoreUnfinished();
    expect(history()).toHaveLength(4);
    expect(fake.calls).not.toContain("resume");
  });

  it("屏幕上已有单文件时，占名额的那份也列在“最近”，不抢屏幕", async () => {
    await reviewing();
    const runId = single().runId;
    fake.unfinishedList = [q("b0007", false)];
    await restoreUnfinished();
    expect(single().runId).toBe(runId);
    expect(entryOf("b0007")).toMatchObject({ k: "file", park: true });
  });

  it("屏幕上和批次文件背后的任务不再列一次（文件在恢复完成前就拖入了）", async () => {
    await reviewing();
    await runningBatchBeside();
    fake.units = [unit(1, "x", [])];
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(getState().behind?.single.live?.batch).toBe("b0001");
    fake.calls = [];
    fake.unfinishedList = [q("b0001", false), b("b0002", false)];
    await restoreUnfinished();
    expect(history()).toHaveLength(0);
    expect(fake.calls).not.toContain("resume");
  });

  it("屏幕上的批次不再列一次", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    fake.calls = [];
    fake.unfinishedList = [b("b0001", false)];
    await restoreUnfinished();
    expect(history()).toHaveLength(0);
  });

  it("屏幕上的单文件不再列一次", async () => {
    await reviewing();
    fake.calls = [];
    fake.unfinishedList = [q("b0001", false)];
    await restoreUnfinished();
    expect(history()).toHaveLength(0);
    expect(fake.calls).not.toContain("resume");
  });

  it("重启后列出的单文件选择继续时才恢复，并读出文档", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
    fake.unfinishedList = [q("b0001", true)];
    fake.rows = [fake.row(0, "awaiting_review", { format: "txt" })];
    await restoreUnfinished();
    expect(getState().single).toBeNull();
    expect(fake.resumed).toEqual([]);
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(fake.resumed).toEqual(["b0001"]));
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(single().hits).toHaveLength(1);
    expect(single().out).toBe("/Users/x/Desktop/Tacet 已脱敏");
    expect(entryOf("b0001")).toBeUndefined();
  });
});

describe("关闭与清理", () => {
  it("关闭提示列出屏幕上和停放的全部任务", async () => {
    await reviewing();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    takePaths(["/d/c.txt"]);
    await waitFor(() => expect(single().name).toBe("c.txt"));
    act(() => requestQuit());
    expect(overlayStore.get().modal).toMatchObject({ type: "closeWindow" });
    const text = document.querySelector("#layer .modal")!.textContent!;
    for (const n of ["a.txt", "b.txt", "c.txt"]) expect(text).toContain(n);
  });

  it("屏幕上只剩待处理文件的批次也算未完成：关闭前要问", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    act(() => requestQuit());
    expect(overlayStore.get().modal).toMatchObject({ type: "closeWindow" });
    expect(document.querySelector("#layer .modal")!.textContent).toContain(batch().title);
    let quit = 0;
    setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit++; } } as never);
    act(() => stopAndQuit());
    await waitFor(() => expect(quit).toBe(1));
    expect(fake.cancelled).toEqual(["b0001"]);
    expect(getState().batch).toBeNull();
  });

  it("暂停保存的批次也算未完成：关闭前要问，并写明它的状态", async () => {
    await runningBatch(["extracting_detecting"], ["queued"]);
    await stopLiveBatch(true);
    act(() => requestQuit());
    expect(overlayStore.get().modal).toMatchObject({ type: "closeWindow" });
    expect(document.querySelector("#layer .modal")!.textContent).toContain("已暂停 · 可继续");
  });

  it("“停止并清理”退出：引擎里的任务也都停止并清理，下次不再出现", async () => {
    await reviewing();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    let quit = 0;
    setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit++; } } as never);
    act(() => stopAndQuit());
    await waitFor(() => expect(quit).toBe(1));
    expect([...fake.cancelled].sort()).toEqual(["b0001", "b0002"]);
    expect([...fake.cleaned].sort()).toEqual(["b0001", "b0002"]);
    expect(history().some((h) => h.park)).toBe(false);
  });

  it("“停止并清理”一个停放的任务：引擎确实取消并释放；引擎拒绝时它还留在列表里", async () => {
    await reviewing();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    const id = entryOf("b0001")!.id;
    const realCancel = fake.cancel.bind(fake);
    fake.cancel = async () => { throw new Error("ACTION_NOT_ALLOWED"); };
    await act(async () => { await discardSavedTask(id); });
    expect(entryOf("b0001")).toBeTruthy();
    expect(fake.cleaned).toEqual([]);
    fake.cancel = realCancel;
    await act(async () => { await discardSavedTask(id); });
    expect(fake.cancelled).toEqual(["b0001"]);
    expect(fake.cleaned).toEqual(["b0001"]);
    expect(entryOf("b0001")).toBeUndefined();
  });

  it("设置里的“清理”：可恢复的批次在引擎里也被清理", async () => {
    await runningBatch(["extracting_detecting"], ["queued"]);
    await stopLiveBatch(true);
    act(() => cleanWorkspace());
    await waitFor(() => expect(fake.cleaned).toEqual(["b0001"]));
    expect(fake.cancelled).toEqual(["b0001"]);
    expect(entryOf("b0001")).toBeUndefined();
  });
});

describe("从“最近”回到停放的批次", () => {
  /** b0001, parked with `states`; b0002, a draft, is what took its place on screen. */
  async function parkedBatch(...states: [string, object?][]) {
    await runningBatch(...states);
    await waitFor(() => expect(batch().phase).toBe("result"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
  }

  it("点开只剩待复核文件的批次：进继续/清理弹窗，不是结果页", async () => {
    await parkedBatch(["awaiting_review", { pending: 1 }], ["awaiting_review", { pending: 1 }]);
    const h = entryOf("b0001")!;
    expect(h.stl).toContain("已导出 0 / 2");
    act(() => openHistoryEntry(h.id));
    expect(overlayStore.get().modal).toMatchObject({ type: "resumeBatch", entryId: h.id });
    expect(getState().view).not.toBe("history");
  });

  it("点开已全部交付的结果：仍是结果页（对照）", async () => {
    await parkedBatch(["completed"], ["completed"]);
    act(() => openHistoryEntry(entryOf("b0001")!.id));
    expect(overlayStore.get().modal).toBeNull();
    expect(getState().view).toBe("history");
  });

  it("结果页的“去处理”（屏幕上的批次）：打开文件，决定交给引擎，不进演示复核", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    fake.units = [unit(1, "x", [{ id: 31, start: 0, end: 1 }])];
    act(() => openHistoryEntry("live-batch"));
    expect(getState().view).toBe("history");
    act(() => startReview(null));
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(batch().phase).toBe("result");
    expect(batch().rv).toBeUndefined();
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    act(() => returnToBatch());
    await waitFor(() => expect(fake.decisions).toEqual([{ id: 31, keep: true }]));
    expect(fake.decisionLog[0]!.job).toBe("j1");
  });

  it("结果页的“去处理”（已收起的批次）：先恢复批次，再打开文件", async () => {
    await parkedBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await fake.emitFor("b0002", rowsOf(["preflighted"], ["preflighted"]));
    await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
    startBatch();
    await waitFor(() => expect(batch().phase).toBe("run"));
    await fake.emitFor("b0002", rowsOf(["completed"], ["completed"]));
    await waitFor(() => expect(batch().phase).toBe("result"));
    fake.units = [unit(1, "x", [{ id: 31, start: 0, end: 1 }])];
    const h = entryOf("b0001")!;
    act(() => openHistoryResult(h));
    act(() => startReview(null));
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(fake.resumed).toEqual(["b0001"]);
    expect(batch().live!.id).toBe("b0001");
    expect(batch().rv).toBeUndefined();
    expect(entryOf("b0001")).toBeUndefined();
  });

  it("批次文件里点“还有 N 处待你处理”：留在内核的流程里，不进演示复核", async () => {
    await runningBatch(["awaiting_review", { pending: 1 }], ["completed"]);
    await waitFor(() => expect(batch().phase).toBe("result"));
    fake.units = [unit(1, "x", [{ id: 31, start: 0, end: 1 }])];
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    act(() => reviewBatchFileExceptions());
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(batch().phase).toBe("result");
    expect(batch().rv).toBeUndefined();
  });

  describe("失败的文件：内核没有结束它，批次也没有结束", () => {
    const failedAndDelivered: [string, object?][] = [["failed", { detail: "EXPORT_FAILED" }], ["completed"]];

    it("新批次进来：它进“最近”时是可继续的任务，不是结果", async () => {
      await parkedBatch(...failedAndDelivered);
      const h = entryOf("b0001")!;
      expect(h).toMatchObject({ st: "keep", park: true });
      expect(h.stl).toContain("待你处理 1 份");
      act(() => openHistoryEntry(h.id));
      expect(overlayStore.get().modal).toMatchObject({ type: "resumeBatch" });
    });

    it("退出选“停止并清理”：两个批次都停止", async () => {
      await parkedBatch(...failedAndDelivered);
      let quit = 0;
      setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit++; } } as never);
      act(() => stopAndQuit());
      await waitFor(() => expect(quit).toBe(1));
      expect([...fake.cancelled].sort()).toEqual(["b0001", "b0002"]);
    });

    it("屏幕上的批次：关闭前要问；结果页有路可走，重试到达引擎", async () => {
      await runningBatch(...failedAndDelivered);
      await waitFor(() => expect(batch().phase).toBe("result"));
      act(() => requestQuit());
      expect(overlayStore.get().modal).toMatchObject({ type: "closeWindow" });
      act(() => closeModal());
      expect(win()).toContain("其中 1 份待你处理");
      act(() => buttons("去处理")[0]!.click());
      expect(overlayStore.get().modal).toMatchObject({ type: "liveBlocked" });
      act(() => buttons("重新处理", document.querySelector("#layer .modal")!)[0]!.click());
      await waitFor(() => expect(fake.calls).toContain("retry"));
    });

    it("无法处理的文件是真的结束了：批次仍算结果（对照）", async () => {
      await parkedBatch(["unprocessable", { detail: "PROBE_FAILED" }], ["completed"]);
      expect(entryOf("b0001")).toMatchObject({ st: "part" });
      expect(entryOf("b0001")!.park).toBeUndefined();
    });
  });
});

describe("单文件停在受阻或失败上", () => {
  const listed = (state: string, detail: string) => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
    fake.rows = [fake.row(0, state, { detail, format: "txt" })];
    fake.unfinishedList = [{ id: "b0001", kind: "quick", phase: "running", total: 1, counts: {}, delivered: 0, exit_code: 2, started: true, parked: true, files: fake.rows }];
  };

  for (const [state, detail] of [["blocked", "PUBLISH_NAME_CONFLICT"], ["failed", "EXPORT_FAILED"]] as const) {
    it("重启后继续 " + state + "：进复核而不是清理，导出会重试", async () => {
      listed(state, detail);
      await restoreUnfinished();
      act(() => resumeSavedBatch(entryOf("b0001")!.id));
      await waitFor(() => expect(single()?.phase).toBe("review"));
      expect(fake.cancelled).toEqual([]);
      expect(fake.cleaned).toEqual([]);
      expect(single().hits).toHaveLength(1);
      await waitFor(() => expect(toastText()).toContain("可重试导出"));
      exportSingle();
      await waitFor(() => expect(fake.retried).toEqual([["b0001", ["j1"]]]));
      expect(fake.decisions).toEqual([{ id: 11, keep: false }]);
      await fake.emit("completed", { output: "f0001.txt" });
      await waitFor(() => expect(single().phase).toBe("done"));
    });
  }

  it("占名额的那份重启后受阻：同样不清理", async () => {
    listed("blocked", "PUBLISH_NAME_CONFLICT");
    fake.unfinishedList[0]!.parked = false;
    await restoreUnfinished();
    await waitFor(() => expect(single()?.phase).toBe("review"));
    expect(fake.cancelled).toEqual([]);
    await waitFor(() => expect(toastText()).toContain("可重试导出"));
  });

  it("原文件变了：内核重试不了，不说“可重试”，也不清理", async () => {
    listed("blocked", "INPUT_CHANGED");
    await restoreUnfinished();
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(single()?.phase).toBe("review"));
    await waitFor(() => expect(toastText()).toContain("原文件已变化"));
    expect(toastText()).not.toContain("可重试");
    expect(fake.cancelled).toEqual([]);
  });

  it("导出时失败：回到复核，选择都在，可以再导出", async () => {
    await reviewing();
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; }));
    exportSingle();
    await waitFor(() => expect(single().phase).toBe("exporting"));
    await fake.emit("failed", { detail: "EXPORT_FAILED" });
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(single().hits[0]!.state).toBe("keep");
    expect(fake.cancelled).toEqual([]);
    expect(toastText()).toContain("可重试导出");
  });

  it("读出文档之前就失败：没有可保留的内容，名额还给下一个（对照）", async () => {
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    fake.units = [];
    await fake.emit("failed", { detail: "EXTRACT_FAILED" });
    await waitFor(() => expect(getState().single).toBeNull());
    expect(fake.cancelled).toEqual(["b0001"]);
  });

  it("文件真的结束了（无法处理）：照旧收尾（对照）", async () => {
    await reviewing();
    await fake.emit("unprocessable", { detail: "PROBE_FAILED" });
    await waitFor(() => expect(getState().single).toBeNull());
    expect(fake.cancelled).toEqual(["b0001"]);
  });
});

describe("单文件的选择要撑过重启", () => {
  const restart = async () => {
    act(() => resetStore());
    fake.unfinishedList = [{ id: "b0001", kind: "quick", phase: "running", total: 1, counts: {}, delivered: 0, exit_code: 2, started: true, parked: true, files: [fake.row(0, "paused", { format: "txt" })] }];
    await restoreUnfinished();
    fake.rows = [fake.row(0, "awaiting_review", { format: "txt" })];
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(single()?.phase).toBe("review"));
  };
  const keepIt = () => act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));

  it("收起 → 重启 → 决定仍在", async () => {
    await reviewing();
    keepIt();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.decisionLog).toHaveLength(1));
    expect(fake.decisionLog[0]).toEqual({ job: "j1", ds: [{ id: 11, keep: true }] });
    await restart();
    expect(single().hits[0]!.state).toBe("keep");
  });

  it("什么都没改就收起：不打扰内核，文件不重做（对照）", async () => {
    await reviewing();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await new Promise((r) => setTimeout(r, 10));
    expect(fake.decisionLog).toEqual([]);
  });

  it("撤销了已交给内核的选择，再收起：内核也要改回来", async () => {
    await reviewing();
    keepIt();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(single().name).toBe("a.txt"));
    act(() => store.update((d) => { d.single!.hits[0]!.state = "redact"; d.undo = []; }));
    takePaths(["/d/c.txt"]);
    await waitFor(() => expect(single().name).toBe("c.txt"));
    await waitFor(() => expect(fake.decisionLog.at(-1)).toEqual({ job: "j1", ds: [{ id: 11, keep: false }] }));
  });

  it("“保存并退出”：决定先到内核，再退出", async () => {
    await reviewing();
    keepIt();
    let quit = 0;
    setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit++; } } as never);
    let release = () => {};
    fake.decideGate = new Promise((r) => { release = r; });
    act(() => saveAndQuit());
    await new Promise((r) => setTimeout(r, 10));
    expect(quit).toBe(0);
    release();
    await waitFor(() => expect(quit).toBe(1));
    expect(fake.decisionLog[0]).toEqual({ job: "j1", ds: [{ id: 11, keep: true }] });
  });

  it("“保存并退出”时内核拒绝：不退出，并说明", async () => {
    await reviewing();
    keepIt();
    let quit = 0;
    setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit++; } } as never);
    fake.decideFails = new Error("NOT_FOUND");
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    expect(quit).toBe(0);
  });

  it("“保存并退出”时批次文件开着：它和背后单文件的选择都交给内核", async () => {
    await reviewing();
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    await runningBatchBeside();
    fake.units = [unit(1, "x", [{ id: 31, start: 0, end: 1 }])];
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    act(() => store.update((d) => { d.single!.hits[0]!.state = "keep"; d.undo = [{} as never]; }));
    let quit = 0;
    setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit++; } } as never);
    act(() => saveAndQuit());
    await waitFor(() => expect(quit).toBe(1));
    expect(fake.decisionLog.flatMap((x) => x.ds).map((d) => d.id).sort()).toEqual([11, 31]);
  });
});

describe("设置里的“清理”", () => {
  const parkedSingle = async () => {
    await reviewing();
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
  };

  it("停放的单文件：对话框列出它，内核里也被清理", async () => {
    await parkedSingle();
    act(() => openModal({ type: "cleanWorkspace" }));
    expect(document.querySelector("#layer .modal")!.textContent).toContain("a.txt");
    act(() => { closeModal(); store.update((d) => { d.view = "settings"; }); });
    expect(win()).toContain("1 可恢复任务");
    act(() => cleanWorkspace());
    await waitFor(() => expect(fake.cleaned).toEqual(["b0001"]));
    expect(fake.cancelled).toEqual(["b0001"]);
    await waitFor(() => expect(entryOf("b0001")).toBeUndefined());
    expect(toastText()).toContain("已清理");
  });

  it("内核拒绝：条目留在列表，提示的是拒绝，不是“已清理”", async () => {
    await parkedSingle();
    fake.cancel = async () => { throw new Error("ACTION_NOT_ALLOWED"); };
    act(() => cleanWorkspace());
    await waitFor(() => expect(toastText()).toContain("ACTION_NOT_ALLOWED"));
    expect(entryOf("b0001")).toBeTruthy();
    await new Promise((r) => setTimeout(r, 10));
    expect(toastText()).not.toContain("已清理");
  });

  it("一个成功一个被拒：成功的移除，被拒的留下", async () => {
    await parkedSingle();
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    takePaths(["/d/c.txt"]);
    await waitFor(() => expect(single().name).toBe("c.txt"));
    const realCancel = fake.cancel.bind(fake);
    fake.cancel = async (b?: string) => { if (b === "b0001") throw new Error("ACTION_NOT_ALLOWED"); return realCancel(b); };
    act(() => cleanWorkspace());
    await waitFor(() => expect(entryOf("b0002")).toBeUndefined());
    expect(entryOf("b0001")).toBeTruthy();
    expect(toastText()).not.toContain("已清理");
  });
});

describe("单文件等密码", () => {
  it("关掉密码弹窗后拖入新文件：直接让路，继续时再问密码", async () => {
    takePaths(["/d/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("locked");
    expect(overlayStore.get().modal).toMatchObject({ type: "livePassword" });
    act(() => closeModal());
    fake.calls = [];
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    expect(imports()).toEqual(["import:quick:1"]);
    expect(toastText()).toContain("已保存到“最近”");
    expect(entryOf("b0001")).toMatchObject({ park: true, stl: "需要密码" });
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("ready_to_publish");
    await waitFor(() => expect(single().phase).toBe("review"));
    fake.kept.set("b0001", [fake.row(0, "locked")]);
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(overlayStore.get().modal).toMatchObject({ type: "livePassword", targets: [{ job: "j1", name: "a.pdf" }] }));
  });

  it("密码弹窗开着时拖入新文件：弹窗随旧任务离开，不挡住新任务", async () => {
    takePaths(["/d/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("locked");
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    expect(overlayStore.get().modal).toBeNull();
  });

  it("输入密码后继续处理：又算在处理中，不让路（对照）", async () => {
    takePaths(["/d/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("locked");
    await fake.emit("extracting_detecting");
    fake.calls = [];
    takePaths(["/d/b.txt"]);
    expect(imports()).toEqual([]);
    expect(toastText()).toContain("正在处理");
  });
});
