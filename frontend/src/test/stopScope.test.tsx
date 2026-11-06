// #41: with choices the kernel refused, “停止并清理” costs the file or task they were on, not every unfinished task.
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { openBatchFile, startBatch } from "../actions/batch";
import { sendDecisions } from "../actions/decisions";
import { discardSavedTask, resumeSavedBatch } from "../actions/home";
import { restoreUnfinished, startLive, stopLiveBatch, takePaths } from "../actions/live";
import { focusHit, returnToBatch, setHitState } from "../actions/single";
import { requestQuit, saveAndQuit, stopAndQuit } from "../actions/window";
import { closeModal, dismissToast, overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { getState, resetStore, store } from "../store";
import { FakeEngine, unit } from "./liveFake";
import { button, mountApp } from "./helpers";
import type { Batch } from "../domain/types";

beforeAll(mountApp);

let fake: FakeEngine;
let stop: () => void;
let quit: { n: number };

beforeEach(() => {
  act(() => { resetStore(); closeModal(); });
  fake = new FakeEngine();
  quit = { n: 0 };
  setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit.n++; } } as never);
  stop = startLive();
});
afterEach(() => {
  stop();
  setService(null);
});

const single = () => getState().single!;
const batch = () => getState().batch!;
const toastText = () => overlayStore.get().toast?.msg ?? "";
const dialog = () => document.querySelector("#layer .modal")!.textContent!.replace(/\s+/g, " ");
const settle = () => new Promise((r) => setTimeout(r, 20));
const statesOf = (id: string) => fake.statusFor(id).files!.map((f) => f.state);
const choose = (i: number, state: "keep" | "redact") => act(() => { focusHit(i); setHitState(state); });
const decides = () => fake.calls.filter((c) => c === "decide").length;
const entryOf = (id: string) => getState().history.find((h) => (h.saved as { live?: { id?: string; batch?: string } } | undefined)?.live?.[(h.k === "batch" ? "id" : "batch")] === id);
const refuse = (reason = "STORE_FAILED") => { fake.decideFails = new Error(reason); };
const accept = () => { fake.decideFails = null; };
const gate = () => { let open = () => {}; const p = new Promise<void>((r) => { open = r; }); return [p, open] as const; };

/** A started batch on screen, its files with ids of their own: the fake's default ids are the same in every batch. */
async function batchOn(prefix: string, names: string[], ...states: string[]) {
  const rows = (s: string[]) => s.map((state, i) => fake.row(i, state, { id: prefix + (i + 1), pending: state === "awaiting_review" ? 1 : 0 }));
  const preflighted = rows(states.map(() => "preflighted"));
  if (getState().batch || fake.calls.some((c) => c.startsWith("import"))) fake.nextRows = preflighted; else fake.rows = preflighted;
  const before = getState().batch?.live?.id;
  takePaths(names);
  await waitFor(() => { expect(batch()?.live?.id).toBeTruthy(); expect(batch().live!.id).not.toBe(before); });
  await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
  const id = batch().live!.id;
  act(() => startBatch());
  await waitFor(() => expect(batch().phase).toBe("run"));
  await act(() => fake.emitFor(id, rows(states)));
  await waitFor(() => expect(batch().live!.jobs[0]).toBe(prefix + "1"));
  return id;
}

/** A single task in review on screen; the task it replaces goes to “最近”. */
async function quickOn(job: string, path: string) {
  fake.units = [unit(1, "电话 13812345678", [{ id: 51, start: 3, end: 14 }])];
  if (fake.calls.some((c) => c.startsWith("import"))) fake.nextRows = [fake.row(0, "awaiting_review", { id: job, pending: 1 })];
  else fake.rows = [fake.row(0, "awaiting_review", { id: job, pending: 1 })];
  takePaths([path]);
  await waitFor(() => { expect(single()?.name).toBe(path.split("/").pop()); expect(single().phase).toBe("review"); });
  return single().live!.batch;
}

/** Opens a file of the batch on screen, makes a choice and leaves it. `meanwhile` is what happens before it leaves. */
async function chooseOnFile(fi: number, state: "keep" | "redact", meanwhile?: () => void) {
  fake.units = [unit(1, "电话 13812345678", [{ id: 31 + fi, start: 3, end: 14 }])];
  const job = batch().live!.jobs[fi];
  act(() => openBatchFile(fi));
  await waitFor(() => expect(single()?.live?.job).toBe(job));
  choose(0, state);
  meanwhile?.();
  const before = decides();
  act(() => returnToBatch());
  await waitFor(() => expect(decides()).toBe(before + 1));
  await settle();
}

/** What the batch of `prefix` looks like after a write on its first file was refused and a write on its second landed. */
async function batchWithARefusedFile(prefix: string, names: string[]) {
  const id = await batchOn(prefix, names, "awaiting_review", "awaiting_review");
  await chooseOnFile(1, "keep");
  await act(() => fake.emitFor(id, [fake.row(0, "awaiting_review", { id: prefix + "1", pending: 1 }), fake.row(1, "awaiting_review", { id: prefix + "2", pending: 1 })]));
  refuse();
  await chooseOnFile(0, "keep");
  accept();
  return id;
}

describe("写入失败时“停止并清理”只清理受影响的", () => {
  it("多个批次里只有一个文件写入失败：只清理这个文件；其余批次、“最近”和同批其他文件的状态与选择都在，下次能接着做", async () => {
    const first = await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    const second = await batchOn("b", ["/d/b1.txt", "/d/b2.txt"], "awaiting_review", "extracting_detecting");
    await chooseOnFile(0, "keep");
    await act(() => fake.emitFor(second, [fake.row(0, "awaiting_review", { id: "b1", pending: 1 }), fake.row(1, "extracting_detecting", { id: "b2" })]));
    const parked = await quickOn("q1", "/d/q1.txt");
    choose(0, "keep");
    const onScreen = await quickOn("q2", "/d/q2.txt"); // the one before it goes to “最近”, with its choice
    await waitFor(() => expect(fake.decisionsHeld("q1")).toEqual({ 51: true }));
    choose(0, "keep"); // only this window holds it: it has to reach the kernel before the window goes

    const listed = () => getState().history.filter((h) => h.park).map((h) => [h.id, h.name, h.st, h.stl]);
    const listedBefore = listed();
    expect(listedBefore).toHaveLength(2);
    expect(fake.decisionsHeld("a2")).toEqual({ 32: true }); // what “still there” below is compared with
    expect(fake.decisionsHeld("b1")).toEqual({ 31: true });

    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));

    // no task stopped as a whole, and the rest of the batch and the other tasks are as they were, with their choices
    expect(fake.cancelled).toEqual([]);
    expect(fake.cleaned).toEqual([]);
    expect(fake.cancelledFiles).toEqual(["a1"]);
    expect(fake.cleanedFiles).toEqual(["a1"]);
    expect(statesOf(first)).toEqual(["cancelled", "awaiting_review"]);
    expect(fake.decisionsHeld("a2")).toEqual({ 32: true });
    expect(statesOf(second)).toEqual(["awaiting_review", "extracting_detecting"]);
    expect(fake.decisionsHeld("b1")).toEqual({ 31: true });
    expect(statesOf(parked)).toEqual(["awaiting_review"]);
    expect(fake.decisionsHeld("q1")).toEqual({ 51: true });
    // the list and the screen too; what only the window held is with the kernel now
    expect(listed()).toEqual(listedBefore);
    expect(batch().live!.id).toBe(second);
    expect(batch().paused).toBe(true); // as “保存并关闭” leaves it
    expect(single().live!.batch).toBe(onScreen);
    expect(fake.decisionsHeld("q2")).toEqual({ 51: true });

    // the next start: they are all listed, and go on from where they were
    stop();
    act(() => resetStore());
    stop = startLive();
    fake.unfinishedList = [first, second, parked, onScreen].map((id) => fake.statusFor(id));
    await act(() => restoreUnfinished());
    await waitFor(() => expect(entryOf(second)).toBeTruthy());
    expect(entryOf(first)).toBeTruthy();
    expect(entryOf(parked)).toBeTruthy();
    expect(single().live!.batch).toBe(onScreen);
    expect((entryOf(first)!.saved as Batch).files.map((f) => f.st)).toEqual(["skipped", "held"]);
    const id = entryOf(second)!.id;
    act(() => resumeSavedBatch(id));
    await waitFor(() => expect(fake.resumed).toContain(second));
    expect(batch().files[0]!.st).toBe("held");
  });

  it("单文件任务写入失败：它作为一个任务被清理，屏幕上的批次和它的选择都在", async () => {
    const kept = await batchOn("b", ["/d/b1.txt", "/d/b2.txt"], "awaiting_review", "awaiting_review");
    await chooseOnFile(0, "keep");
    const statesBefore = statesOf(kept);
    const task = await quickOn("q1", "/d/q1.txt");
    choose(0, "keep");
    refuse();
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    expect(quit.n).toBe(0);
    accept();
    act(() => requestQuit());
    expect(dialog()).toContain("q1.txt");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cancelled).toEqual([task]);
    expect(fake.cleaned).toEqual([task]);
    expect(fake.cancelledFiles).toEqual([]);
    expect(getState().single).toBeNull();
    expect(getState().undo).toEqual([]);
    expect(getState().view).toBe("home");
    expect(batch().live!.id).toBe(kept);
    expect(statesOf(kept)).toEqual(statesBefore);
    expect(fake.decisionsHeld("b1")).toEqual({ 31: true });
  });

  it("“最近”里的单文件写入失败：清掉它的条目，其他条目不变", async () => {
    const refused = await quickOn("q1", "/d/q1.txt");
    choose(0, "keep");
    refuse();
    const kept = await quickOn("q2", "/d/q2.txt"); // q1 leaves the screen with its choice, which is refused
    await waitFor(() => expect(decides()).toBe(1));
    await settle();
    accept();
    const other = await quickOn("q3", "/d/q3.txt"); // q2 goes to “最近” untouched
    expect(entryOf(refused)).toBeTruthy();
    expect(entryOf(kept)).toBeTruthy();
    act(() => requestQuit());
    expect(dialog().split("保留：")[1]).toContain("q2.txt");
    expect(dialog().split("保留：")[1]).not.toContain("q1.txt");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cancelled).toEqual([refused]);
    expect(entryOf(refused)).toBeUndefined();
    expect(entryOf(kept)).toMatchObject({ name: "q2.txt", park: true });
    expect(single().live!.batch).toBe(other);
  });

  it("点“停止并清理”后才失败的写入：只清理它，不按对话框说的全部清理", async () => {
    const first = await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    const job = batch().live!.jobs[0]!;
    fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14 }])];
    act(() => openBatchFile(0));
    await waitFor(() => expect(single()?.live?.job).toBe(job));
    choose(0, "keep");
    const [held, release] = gate();
    fake.decideGateOf.set(job, held);
    act(() => returnToBatch());
    const second = await batchOn("b", ["/d/b1.txt", "/d/b2.txt"], "awaiting_review", "awaiting_review");
    act(() => requestQuit());
    expect(dialog()).toContain("下列全部任务");
    act(() => stopAndQuit());
    await settle();
    expect(fake.cancelled).toEqual([]);
    expect(fake.cancelledFiles).toEqual([]);
    refuse();
    release();
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cancelledFiles).toEqual(["a1"]);
    expect(fake.cancelled).toEqual([]);
    expect(statesOf(first)).toEqual(["cancelled", "awaiting_review"]);
    expect(statesOf(second)).toEqual(["awaiting_review", "awaiting_review"]);
  });

  it("没有写入失败时（对照）：列出的任务全部清理", async () => {
    const first = await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    const second = await batchOn("b", ["/d/b1.txt", "/d/b2.txt"], "awaiting_review", "awaiting_review");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect([...fake.cleaned].sort()).toEqual([first, second]);
    expect(fake.cancelledFiles).toEqual([]);
    expect(entryOf(first)).toBeUndefined();
  });

  it("对话框点名的文件，之后写入成功了也照样清理：说了清理它", async () => {
    await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    refuse();
    await chooseOnFile(0, "keep");
    fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14 }])];
    act(() => openBatchFile(0));
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "redact"); // still open: stopping sends it once more, and this time it lands
    accept();
    act(() => requestQuit());
    expect(dialog()).toContain("a1.txt");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.decisionLog.at(-1)).toMatchObject({ job: "a1" });
    expect(fake.cleanedFiles).toEqual(["a1"]);
    expect(fake.cleaned).toEqual([]);
  });

  it("清理时才被拒绝的、屏幕上其他任务的写入：也算受影响，一起清理，窗口没了它就没了", async () => {
    await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    const task = await quickOn("q1", "/d/q1.txt");
    choose(0, "keep");
    refuse();
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cleanedFiles).toEqual(["a1"]);
    expect(fake.cleaned).toEqual([task]);
    expect(fake.decisionsHeld("a2")).toEqual({ 32: true });
  });

  it("批次文件开着，等在它后面的单文件写入失败：清理的是单文件，批次留下", async () => {
    const kept = await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    const task = await quickOn("q1", "/d/q1.txt");
    choose(0, "keep");
    refuse();
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("只清理「q1.txt」"));
    accept();
    fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14 }])];
    act(() => openBatchFile(0));
    await waitFor(() => expect(getState().behind?.single.live?.batch).toBe(task));
    act(() => requestQuit());
    expect(dialog()).toContain("将清理：q1.txt");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cancelled).toEqual([task]);
    expect(fake.cancelledFiles).toEqual([]);
    expect(batch().live!.id).toBe(kept);
  });

  it("内核拒绝清理单文件：屏幕和“最近”的条目都留着，下次启动内核还会列出它", async () => {
    const parked = await quickOn("q1", "/d/q1.txt");
    choose(0, "keep");
    refuse();
    const onScreen = await quickOn("q2", "/d/q2.txt");
    choose(0, "keep");
    fake.cancel = async () => { throw new Error("WORKSPACE_BUSY"); };
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(toastText()).toContain("工作区正被另一个 Tacet 使用");
    expect(entryOf(parked)).toBeTruthy();
    expect(single().live!.batch).toBe(onScreen);
  });

  it("文件的阶段还没停：等它停下再清理，一次成功；此前不退出", async () => {
    const first = await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    fake.stageStopping = true;
    act(() => stopAndQuit());
    await waitFor(() => expect(fake.cancelledFiles).toEqual(["a1"]));
    expect(statesOf(first)).toEqual(["cancelling", "awaiting_review"]);
    await settle();
    expect(fake.cleanedFiles).toEqual([]);
    expect(quit.n).toBe(0);
    fake.stageStopped(first);
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cleanedFiles).toEqual(["a1"]);
  });

  it("内核拒绝清理：说明原因，文件还在内核，别的任务也不动；照样退出", async () => {
    const first = await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    fake.cancelFile = async () => { throw new Error("WORKSPACE_BUSY"); };
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(toastText()).toContain("工作区正被另一个 Tacet 使用");
    expect(statesOf(first)).toEqual(["awaiting_review", "awaiting_review"]);
    expect(fake.cancelled).toEqual([]);
  });

  it("清理后文件不再挡住这个批次：它的失败记录没了，其余文件可以导出", async () => {
    const id = await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cleanedFiles).toEqual(["a1"]);
    await act(() => fake.emitFor(id, [fake.row(0, "cancelled", { id: "a1" }), fake.row(1, "ready_to_publish", { id: "a2" })]));
    await waitFor(() => expect(batch().files[1]!.st).toBe("ready"));
    act(() => store.update((d) => { d.batch!.autoPub = false; }));
    act(() => button("导出 1 份").click());
    act(() => button("导出").click());
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(overlayStore.get().modal).toBeNull();
    act(() => requestQuit());
    expect(dialog()).not.toContain("未能保存");
  });
});

describe("内核说太晚了（DECISIONS_CLOSED）不算写入失败：文档已开始导出，重试不会成功，不因它缩小清理范围", () => {
  /** The kernel goes ahead of the screen: the export of this file starts while the person is still looking at it. */
  const publishingOn = (job: string) => () => { fake.rows = fake.rows.map((r) => (r.id === job ? { ...r, state: "publishing" } : r)); };

  it("只有太晚的文件：对话框说全部任务，“停止并清理”按整个批次走，不单独取消这个文件", async () => {
    const id = await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    await chooseOnFile(0, "keep", publishingOn("a1"));
    expect.soft(toastText()).toContain("这次修改不会生效"); // the kernel did refuse it: what follows is not for want of a refusal

    act(() => requestQuit());
    expect.soft(dialog()).toContain("停止将清理下列全部任务");
    expect.soft(dialog()).not.toContain("未能保存");

    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect.soft(fake.cancelledFiles).toEqual([]);
    expect.soft(fake.cancelled).toEqual([id]);
  });

  it("太晚的和真正写入失败的同时存在：提示和对话框只点名后者，也只清理后者，正在导出的文件不被取消", async () => {
    const id = await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    await chooseOnFile(0, "keep", publishingOn("a1"));
    refuse();
    await chooseOnFile(1, "keep");

    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("窗口未关闭"));
    expect.soft(toastText()).toContain("只清理「a2.txt」，");
    expect(quit.n).toBe(0);

    accept();
    act(() => requestQuit());
    expect.soft(dialog()).toContain("1 个文件的选择未能保存");
    expect.soft(dialog()).toContain("a2.txt");
    expect.soft(dialog()).not.toContain("a1.txt");

    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect.soft(fake.cancelledFiles).toEqual(["a2"]);
    expect.soft(fake.cleanedFiles).toEqual(["a2"]);
    expect.soft(fake.cancelled).toEqual([]);
    expect.soft(statesOf(id)).toEqual(["publishing", "cancelled"]);
  });

  it("对话框开着时，写入失败的文件重写后变成太晚：它跟着回到全部任务的说法，“停止并清理”也回到全部", async () => {
    const id = await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    refuse();
    await chooseOnFile(0, "keep");
    accept();
    act(() => requestQuit());
    expect.soft(dialog()).toContain("1 个文件的选择未能保存"); // what it comes back from

    publishingOn("a1")();
    await act(() => sendDecisions("a1", [{ id: 31, keep: true }]).catch(() => {}));
    await waitFor(() => expect(dialog()).toContain("停止将清理下列全部任务"));
    expect.soft(dialog()).not.toContain("未能保存");

    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect.soft(fake.cancelledFiles).toEqual([]);
    expect.soft(fake.cancelled).toEqual([id]);
  });
});

describe("对话框和提示说明实际的清理范围", () => {
  it("“保存并退出”被拒：提示点名受影响的文件，说其他任务保留，不说全部", async () => {
    await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    act(() => dismissToast());
    refuse();
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    expect(toastText()).toContain("只清理「a1.txt」");
    expect(toastText()).toContain("其他任务保留");
    expect(toastText()).toContain("已导出的文件不受影响");
    expect(toastText()).not.toContain("全部");
    expect(quit.n).toBe(0);
  });

  it("几个文件被拒：提示说一共几个", async () => {
    await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    refuse();
    await chooseOnFile(0, "keep");
    await chooseOnFile(1, "keep");
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    expect(toastText()).toContain("等 2 个文件");
  });

  it("关闭对话框：列出会清理的文件和保留的任务，并说明批次里的文件属于哪个批次", async () => {
    await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    await batchOn("b", ["/d/b1.txt", "/d/b2.txt"], "awaiting_review", "awaiting_review");
    act(() => requestQuit());
    const text = dialog();
    expect(text).toContain("1 个文件的选择未能保存");
    expect(text).toContain("只清理这些文件的中间数据");
    expect(text).not.toContain("全部任务");
    expect(text).toContain("将清理：a1.txt · 2 份文档中的文件");
    expect(text).toContain("保留：");
    expect(text.indexOf("保留：")).toBeLessThan(text.lastIndexOf("2 份文档 ·"));
  });

  it("关闭对话框：要清理的单文件任务不算在“保留”里", async () => {
    await quickOn("q1", "/d/q1.txt");
    choose(0, "keep");
    refuse();
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    act(() => requestQuit());
    const text = dialog();
    expect(text).toContain("将清理：q1.txt");
    expect(text).not.toContain("保留：");
    expect(text).not.toContain("待确认");
  });

  it("对话框开着时写入才失败：它跟着改说法", async () => {
    await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    const job = "a1";
    fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14 }])];
    act(() => openBatchFile(0));
    await waitFor(() => expect(single()?.live?.job).toBe(job));
    choose(0, "keep");
    const [held, release] = gate();
    fake.decideGateOf.set(job, held);
    act(() => returnToBatch());
    act(() => requestQuit());
    expect(dialog()).toContain("下列全部任务");
    refuse();
    await act(async () => { release(); await settle(); });
    expect(dialog()).toContain("只清理这些文件的中间数据");
    expect(dialog()).toContain("a1.txt");
  });

  it("对话框开着时，没有提示的写入有了结果：它也跟着改说法", async () => {
    await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    act(() => requestQuit());
    expect(dialog()).toContain("下列全部任务");
    refuse();
    await act(async () => { await sendDecisions("a1", [{ id: 31, keep: true }]).catch(() => {}); await settle(); });
    expect(dialog()).toContain("将清理：a1.txt");
    accept();
    await act(async () => { await sendDecisions("a1", [{ id: 31, keep: true }]); await settle(); });
    expect(dialog()).toContain("下列全部任务");
  });

  it("重新保存成功后：对话框回到全部任务的说法，“停止并清理”也回到全部", async () => {
    const id = await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    act(() => saveAndQuit());
    await waitFor(() => expect(quit.n).toBe(1)); // the write went again and landed
    act(() => requestQuit());
    expect(dialog()).toContain("下列全部任务");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(2));
    expect(fake.cleaned).toEqual([id]);
    expect(fake.cancelledFiles).toEqual([]);
  });

  it("写入失败的任务已经被清理：它不再影响范围", async () => {
    const first = await batchWithARefusedFile("a", ["/d/a1.txt", "/d/a2.txt"]);
    const second = await batchOn("b", ["/d/b1.txt", "/d/b2.txt"], "awaiting_review", "awaiting_review");
    await act(() => discardSavedTask(entryOf(first)!.id));
    expect(fake.cleaned).toEqual([first]);
    act(() => requestQuit());
    expect(dialog()).toContain("下列全部任务");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cleaned).toEqual([first, second]);
    expect(fake.cancelledFiles).toEqual([]);
  });

  it("没有写入失败（对照）：对话框仍说全部任务", async () => {
    await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    act(() => requestQuit());
    expect(dialog()).toContain("停止将清理下列全部任务");
    expect(dialog()).not.toContain("将清理：");
  });

  it("批次页面的“停止并清理”：提示说清理的是这个批次", async () => {
    const id = await batchOn("a", ["/d/a1.txt", "/d/a2.txt"], "awaiting_review", "awaiting_review");
    const { title } = batch();
    await act(() => stopLiveBatch(false));
    expect(fake.cleaned).toEqual([id]);
    expect(toastText()).toContain("已停止并清理「" + title + "」");
    expect(toastText()).toContain("其他任务保留");
  });
});
