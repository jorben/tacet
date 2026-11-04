// Tasks of a kind side by side (FR-28): the kernel goes on with what left the screen, and “最近” follows it.
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { openBatchFile, startBatch, togglePause } from "../actions/batch";
import { returnToBatch } from "../actions/single";
import { openHistoryEntry, resumeSavedBatch } from "../actions/home";
import { sendDecisions } from "../actions/decisions";
import { startLive, stopLiveBatch, takePaths } from "../actions/live";
import { requestQuit } from "../actions/window";
import type { Batch, Single } from "../domain/types";
import { closeModal, overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { getState, resetStore } from "../store";
import { documentBusy } from "../store/selectors";
import { FakeEngine, unit } from "./liveFake";
import { mountApp } from "./helpers";

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
const toastText = () => overlayStore.get().toast?.msg ?? "";
const entryOf = (id: string) => getState().history.find((h) => (h.saved as { live?: { id?: string; batch?: string } } | undefined)?.live?.[(h.k === "batch" ? "id" : "batch")] === id);
const rowsOf = (...states: [string, object?][]) => states.map(([state, extra], i) => fake.row(i, state, extra));
const gate = () => { let open = () => {}; const p = new Promise<void>((r) => { open = r; }); return [p, open] as const; };

/** A single task under way, and a second one on screen in front of it. */
async function twoSingles() {
  fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
  takePaths(["/d/a.txt"]);
  await waitFor(() => expect(fake.calls).toContain("start"));
  await fake.emit("extracting_detecting");
  takePaths(["/d/b.txt"]);
  await waitFor(() => expect(single().name).toBe("b.txt"));
  await waitFor(() => expect(fake.begunCount()).toBe(2));
}

/** A started batch whose files are in the given states. */
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

/** The first batch went to “最近” while under way; the second is on screen. */
async function twoBatches(...states: [string, object?][]) {
  await runningBatch(...states);
  takePaths(["/d/c.txt", "/d/d.txt"]);
  await waitFor(() => expect(batch().live!.id).toBe("b0002"));
}

describe("单文件在“最近”继续", () => {
  it("等到待确认：条目随内核变化，屏幕不动；继续时读出文档", async () => {
    await twoSingles();
    expect(entryOf("b0001")).toMatchObject({ st: "run", stl: "处理中", park: true });
    await fake.emitFor("b0001", [fake.row(0, "awaiting_review", { pending: 1 })]);
    expect(entryOf("b0001")).toMatchObject({ st: "warn", stl: "待确认", park: true });
    expect(single().name).toBe("b.txt");
    expect(single().phase).toBe("processing");
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(single().name).toBe("a.txt"));
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(single().hits).toHaveLength(1);
    expect(entryOf("b0002")).toMatchObject({ name: "b.txt", park: true });
  });

  it("等密码：条目说明，不弹窗挡住屏幕上的任务", async () => {
    await twoSingles();
    await fake.emitFor("b0001", [fake.row(0, "locked", { detail: "password_required" })]);
    await waitFor(() => expect(entryOf("b0001")).toMatchObject({ st: "warn", stl: "需要密码" }));
    expect(overlayStore.get().modal).toBeNull();
    expect((entryOf("b0001")!.saved as Single).live!.locked).toBe(true);
  });

  it("文件无法处理：条目消失并说明，内核里的任务被清理，屏幕不动", async () => {
    await twoSingles();
    await fake.emitFor("b0001", [fake.row(0, "unprocessable", { detail: "PAGE_LIMIT_EXCEEDED" })]);
    await waitFor(() => expect(fake.cleaned).toEqual(["b0001"]));
    expect(entryOf("b0001")).toBeUndefined();
    expect(toastText()).toContain("超过页数上限");
    expect(toastText()).toContain("a.txt");
    expect(single().name).toBe("b.txt");
  });

  it("在内核里已取消：留给放弃的那条路收尾，事件不抢着移除或报错", async () => {
    await twoSingles();
    await fake.emitFor("b0001", [fake.row(0, "cancelled")]);
    expect(entryOf("b0001")).toMatchObject({ park: true });
    expect(fake.cleaned).toEqual([]);
  });

  it("导出失败：条目回到待确认，继续时回到复核并保留选择", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("awaiting_review", { pending: 1 });
    await waitFor(() => expect(single().phase).toBe("review"));
    const { exportSingle } = await import("../actions/single");
    exportSingle();
    await waitFor(() => expect(single().phase).toBe("exporting"));
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    expect((entryOf("b0001")!.saved as Single).phase).not.toBe("done");
    await fake.emitFor("b0001", [fake.row(0, "failed", { detail: "EXPORT_FAILED" })]);
    expect(entryOf("b0001")).toMatchObject({ st: "warn", stl: "待确认" });
    expect((entryOf("b0001")!.saved as Single).phase).toBe("review");
  });

  it("导出的调用被拒绝时已离开屏幕：条目回到待确认，屏幕上的任务不动", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("awaiting_review", { pending: 1 });
    await waitFor(() => expect(single().phase).toBe("review"));
    const [held, open] = gate();
    fake.decideGate = held;
    fake.publishFails = new Error("OUTPUT_UNAVAILABLE");
    const { exportSingle } = await import("../actions/single");
    exportSingle();
    await waitFor(() => expect(fake.calls).toContain("decide"));
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    expect(entryOf("b0001")).toMatchObject({ st: "run" });
    open();
    await waitFor(() => expect(entryOf("b0001")).toMatchObject({ st: "warn", stl: "待确认" }));
    expect((entryOf("b0001")!.saved as Single).phase).toBe("review");
    expect(single().name).toBe("b.txt");
    expect(single().phase).toBe("processing");
  });

  it("开始被拒绝时已离开屏幕：条目也不留下，屏幕上的任务不动", async () => {
    const [held, open] = gate();
    fake.startGate = held;
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    expect(entryOf("b0001")).toMatchObject({ park: true });
    fake.startFails = new Error("NOT_READY");
    open();
    await waitFor(() => expect(entryOf("b0001")).toBeUndefined());
    expect(fake.cancelled).toEqual(["b0001"]);
    expect(single().name).toBe("b.txt");
  });

  it("第一次开始还没回话就离开屏幕：回话后的状态写到条目上", async () => {
    const [held, open] = gate();
    fake.startGate = held;
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    fake.kept.set("b0001", [fake.row(0, "awaiting_review", { pending: 1 })]);
    open();
    await waitFor(() => expect(entryOf("b0001")).toMatchObject({ st: "warn", stl: "待确认" }));
    expect((entryOf("b0001")!.saved as Single).live!.job).toBe("j1");
  });

  it("文档读到一半离开屏幕：读完不写屏幕，条目仍然得知已待确认", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    const [held, open] = gate();
    fake.documentGate = held;
    await fake.emit("awaiting_review", { pending: 1 });
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(entryOf("b0001")).toMatchObject({ st: "warn", stl: "待确认" }));
    open();
    await new Promise((r) => setTimeout(r, 10));
    expect(single().name).toBe("b.txt");
    expect(single().phase).toBe("processing");
    expect((entryOf("b0001")!.saved as Single).pages).toHaveLength(0);
  });

  it("上一份的文档还在读：屏幕上的这一份照样能读出来", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    const [held, open] = gate();
    fake.documentGate = held;
    await fake.emit("awaiting_review", { pending: 1 });
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    await fake.emit("awaiting_review", { pending: 1 });
    await waitFor(() => expect(single().phase).toBe("review"));
    open();
  });

  it("批次文件关上后，背后的单文件读一次内核：它在文件开着时完成了处理", async () => {
    fake.units = [unit(1, "x", [{ id: 11, start: 0, end: 1 }])];
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("extracting_detecting");
    fake.nextRows = rowsOf(["preflighted"], ["preflighted"]);
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch()?.live?.id).toBe("b0002"));
    await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
    startBatch();
    await waitFor(() => expect(batch().phase).toBe("run"));
    await fake.emitFor("b0002", rowsOf(["awaiting_review", { pending: 1 }], ["completed"]));
    await waitFor(() => expect(batch().files[0]?.g.length).toBeGreaterThan(0));
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(getState().behind?.single.name).toBe("a.txt");
    await fake.emitFor("b0001", [fake.row(0, "awaiting_review", { pending: 1 })]);
    expect(getState().behind?.single.phase).toBe("processing"); // it hears nothing behind the open file
    returnToBatch();
    await waitFor(() => expect(getState().single?.name).toBe("a.txt"));
    await waitFor(() => expect(single().phase).toBe("review"));
  });
});

describe("批次在“最近”继续", () => {
  it("处理中：条目报进度，大批次不为此把文件全读一遍", async () => {
    const many = Array.from({ length: 300 }, (_, i) => fake.row(i, "queued"));
    await twoBatches(["extracting_detecting"], ["queued"]);
    fake.calls = [];
    await fake.emitFor("b0001", many);
    expect(entryOf("b0001")).toMatchObject({ st: "run", stl: "处理中 · 0 / 300", park: true });
    expect(fake.calls.filter((c) => c.startsWith("files:"))).toEqual([]);
    expect(batch().live!.id).toBe("b0002");
  });

  it("全部在等人：读出文件，条目说明待处理，继续时是这些文件", async () => {
    await twoBatches(["extracting_detecting"], ["queued"]);
    await fake.emitFor("b0001", rowsOf(["awaiting_review", { pending: 2 }], ["completed"]));
    await waitFor(() => expect(entryOf("b0001")).toMatchObject({ st: "keep", park: true }));
    expect(entryOf("b0001")!.stl).toContain("待你处理");
    expect((entryOf("b0001")!.saved as Batch).files[1]!.st).toBe("done");
    expect(batch().live!.id).toBe("b0002");
  });

  it("全部完成：变成结果，点开是结果页，不再算未完成", async () => {
    await twoBatches(["extracting_detecting"], ["queued"]);
    await fake.emitFor("b0001", rowsOf(["completed"], ["completed"]));
    const h = entryOf("b0001")!;
    expect(h).toMatchObject({ st: "done", stl: "已导出 2" });
    expect(h.park).toBeUndefined();
    expect((h.saved as Batch).phase).toBe("result");
    expect(toastText()).toContain("已在后台完成");
    act(() => openHistoryEntry(h.id));
    expect(getState().view).toBe("history");
    expect(batch().live!.id).toBe("b0002");
  });

  it("屏幕上的批次不被另一个批次的事件改动（G8）", async () => {
    await twoBatches(["extracting_detecting"], ["queued"]);
    fake.rows = rowsOf(["preflighted"], ["preflighted"]);
    await fake.emitBatch();
    const before = JSON.stringify(batch().files);
    await fake.emitFor("b0001", rowsOf(["completed"], ["completed"]));
    expect(JSON.stringify(batch().files)).toBe(before);
  });

  it("读一个批次的大量文件时，另一个批次的状态不用等", async () => {
    await twoBatches(["extracting_detecting"], ["queued"]);
    const [held, open] = gate();
    fake.filesGate = held;
    const many = Array.from({ length: 300 }, (_, i) => fake.row(i, "completed"));
    await fake.emitFor("b0001", many);
    expect(fake.calls).toContain("files:0");
    await fake.emitFor("b0002", rowsOf(["unprocessable"], ["preflighted"]));
    await waitFor(() => expect(batch().files[0]!.st).toBe("bad"));
    open();
    await waitFor(() => expect(entryOf("b0001")).toMatchObject({ st: "done" }));
  });

  it("未开始的草稿：预检的事件不读文件，继续时才读，并回到预检页", async () => {
    fake.rows = rowsOf(["preflighting"], ["preflighting"]);
    takePaths(["/d/a.txt", "/d/b.txt"]);
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    await fake.emitFor("b0001", rowsOf(["preflighted"], ["unprocessable"]));
    expect(entryOf("b0001")).toMatchObject({ stl: "待开始" });
    act(() => resumeSavedBatch(entryOf("b0001")!.id));
    await waitFor(() => expect(batch().live!.id).toBe("b0001"));
    await waitFor(() => expect(batch().files[1]!.st).toBe("bad"));
    expect(batch().phase).toBe("precheck");
    expect(entryOf("b0002")).toMatchObject({ stl: "待开始" });
  });

  it("预检失败时已离开屏幕：条目不留下，屏幕不动", async () => {
    fake.rows = rowsOf(["preflighting"], ["preflighting"]);
    let answer = (_: Error) => {};
    fake.preflight = () => new Promise((_, reject) => { answer = reject; });
    takePaths(["/d/a.txt", "/d/b.txt"]);
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    await waitFor(() => expect(batch().files.length).toBe(2));
    const pending = answer;
    fake.preflight = async () => fake.statusFor("b0002");
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect(entryOf("b0001")).toBeTruthy();
    pending(new Error("STORE_FAILED"));
    await waitFor(() => expect(entryOf("b0001")).toBeUndefined());
    expect(batch().live!.id).toBe("b0002");
  });

  it("停止并保留，写入落地前已换了批次：它标记为暂停，屏幕上的批次不受影响", async () => {
    await runningBatch(["extracting_detecting"], ["queued"]);
    const [held, open] = gate();
    fake.decideGate = held;
    void sendDecisions("j1", [{ id: 1, keep: true }]).catch(() => {});
    const stopping = stopLiveBatch(true);
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    open();
    await stopping;
    expect(batch().live!.id).toBe("b0002");
    expect(batch().paused).toBe(false);
    expect(entryOf("b0001")).toMatchObject({ st: "keep", stl: "已暂停 · 可继续", park: true });
    expect(getState().history.filter((h) => h.k === "batch")).toHaveLength(1);
  });

  it("停止并清理，写入落地前已换了批次：它的条目被清掉，屏幕上的批次不受影响", async () => {
    await runningBatch(["extracting_detecting"], ["queued"]);
    const [held, open] = gate();
    fake.decideGate = held;
    void sendDecisions("j1", [{ id: 1, keep: true }]).catch(() => {});
    const stopping = stopLiveBatch(false);
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    open();
    await stopping;
    expect(fake.cleaned).toEqual(["b0001"]);
    expect(batch().live!.id).toBe("b0002");
    expect(entryOf("b0001")).toBeUndefined();
  });

  it("暂停被拒绝时已换了批次：回滚的是它的条目，屏幕上的批次不受影响", async () => {
    await runningBatch(["extracting_detecting"], ["queued"]);
    let refuse = (_: Error) => {};
    fake.pause = () => new Promise((_, reject) => { refuse = reject; });
    togglePause();
    expect(batch().paused).toBe(true);
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect((entryOf("b0001")!.saved as Batch).paused).toBe(true);
    expect(entryOf("b0001")).toMatchObject({ st: "keep" });
    refuse(new Error("STORE_FAILED"));
    await waitFor(() => expect((entryOf("b0001")!.saved as Batch).paused).toBe(false));
    expect(entryOf("b0001")).toMatchObject({ st: "run" });
    expect(batch().paused).toBe(false);
  });

  it("开始被拒绝时已换了批次：它回到待开始，屏幕上的批次不受影响", async () => {
    fake.rows = rowsOf(["preflighted"], ["preflighted"]);
    takePaths(["/d/a.txt", "/d/b.txt"]);
    await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
    await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
    const [held, open] = gate();
    fake.startGate = held;
    startBatch();
    await waitFor(() => expect(fake.calls).toContain("start"));
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    fake.startFails = new Error("NOT_READY");
    open();
    await waitFor(() => expect(entryOf("b0001")).toMatchObject({ stl: "待开始" }));
    expect((entryOf("b0001")!.saved as Batch).phase).toBe("precheck");
    expect(batch().live!.id).toBe("b0002");
  });
});

describe("离开屏幕的任务仍然算数", () => {
  it("模型不能在后台还在处理文档时换掉；处理完就可以", async () => {
    await twoSingles();
    await fake.emit("awaiting_review", { pending: 1 });
    await waitFor(() => expect(single().phase).toBe("review"));
    expect(documentBusy(getState())).toBe(true);
    await fake.emitFor("b0001", [fake.row(0, "awaiting_review", { pending: 1 })]);
    expect(documentBusy(getState())).toBe(false);
  });

  it("批次在后台处理时同样算在处理", async () => {
    await twoBatches(["extracting_detecting"], ["queued"]);
    fake.rows = rowsOf(["preflighted"], ["preflighted"]);
    await fake.emitBatch();
    expect(documentBusy(getState())).toBe(true);
    await fake.emitFor("b0001", rowsOf(["completed"], ["completed"]));
    expect(documentBusy(getState())).toBe(false);
  });

  it("退出前要问：后台还在处理的任务也列出", async () => {
    await twoSingles();
    act(() => requestQuit());
    expect(overlayStore.get().modal?.type).toBe("closeWindow");
    expect(document.body.textContent).toContain("a.txt · 处理中");
  });
});
