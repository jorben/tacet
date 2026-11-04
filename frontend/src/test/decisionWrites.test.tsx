// #34: a write of choices the kernel refused stands in the way of anything that goes on from them, until it lands.
// #35: of the files of a batch clicked one after another, only the last click changes the screen.
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { openBatchFile, startBatch } from "../actions/batch";
import { discardSavedTask } from "../actions/home";
import { openLiveGroup, skipLive, startLive, takePaths } from "../actions/live";
import { focusHit, returnToBatch, setHitState } from "../actions/single";
import { saveAndQuit, stopAndQuit } from "../actions/window";
import { closeModal, dismissToast, overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { getState, resetStore, store } from "../store";
import { FakeEngine, unit } from "./liveFake";
import { button, buttons, mountApp } from "./helpers";

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
const modal = () => overlayStore.get().modal;
const toastText = () => overlayStore.get().toast?.msg ?? "";
const page = () => document.body.textContent!.replace(/\s+/g, " ");
const settle = () => new Promise((r) => setTimeout(r, 20));
const opening = () => document.querySelector("[role=status].file-opening")?.textContent ?? null;
const decides = () => fake.calls.filter((c) => c === "decide").length;
const rowsOf = (...states: [string, object?][]) => states.map(([state, extra], i) => fake.row(i, state, extra));
const gate = () => { let open = () => {}; const p = new Promise<void>((r) => { open = r; }); return [p, open] as const; };
const choose = (i: number, state: "keep" | "redact") => act(() => { focusHit(i); setHitState(state); });
const shown = (i: number) => single().hits[i]!.state;
const quitting = () => {
  const q = { n: 0 };
  setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { q.n++; } } as never);
  return q;
};

/** A batch of two started without authorization to publish, as a person who exports by hand has it. */
async function batchOf(paths: string[], ...states: [string, object?][]) {
  fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14 }])];
  fake.rows = rowsOf(["preflighted"], ["preflighted"]);
  takePaths(paths);
  await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
  await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
  act(() => store.update((d) => { d.batch!.autoPub = false; }));
  startBatch();
  await waitFor(() => expect(fake.calls).toContain("start"));
  await waitFor(() => expect(batch().phase).toBe("run"));
  await setRows(...states);
}
async function setRows(...states: [string, object?][]) {
  fake.rows = rowsOf(...states);
  await act(() => fake.emitBatch());
}
const AB = ["/d/a.txt", "/d/b.txt"];

/** The person's button: “导出 N 份” on the batch page, then “导出” in its dialog. */
function exportByHand(n = 1) {
  act(() => button("导出 " + n + " 份").click());
  act(() => button("导出").click());
}

/** Back to the batch from the open file, with the write of its choices refused and over. */
async function refusedOnLeaving() {
  const before = decides();
  fake.decideFails = new Error("STORE_FAILED");
  act(() => returnToBatch());
  await waitFor(() => expect(decides()).toBe(before + 1));
  await settle();
}

/** The kernel holds “keep” on a file ready to publish; the person changes it to “redact” and the write is refused. */
async function refusedOnReadyFile() {
  await batchOf(AB, ["ready_to_publish"], ["completed"]);
  await fake.decide("j1", [{ id: 31, keep: true }]);
  await setRows(["ready_to_publish"], ["completed"]);
  fake.decisionLog = [];
  fake.calls = [];
  openBatchFile(0);
  await waitFor(() => expect(single()?.from).toBe("batch"));
  expect(shown(0)).toBe("keep");
  choose(0, "redact");
  await refusedOnLeaving();
}

describe("#34 选择没有写进内核时，不按旧选择往下走", () => {
  it("写入失败后导出被拒，说明是哪个文件；重新保存成功后可导出，导出的是最后的选择", async () => {
    await refusedOnReadyFile();
    exportByHand();
    await settle();
    expect(fake.calls).not.toContain("publish");
    expect(modal()?.type).toBe("unsavedDecisions");
    expect(page()).toContain("a.txt");
    expect(page()).toContain("重新保存");
    expect(page()).toContain("打开文件");

    // refused again, for another reason: the dialog stays and says the one that holds now, and nothing is exported
    const before = decides();
    fake.decideFails = new Error("WORKSPACE_BUSY");
    act(() => button("重新保存").click());
    await waitFor(() => expect(decides()).toBe(before + 1));
    await waitFor(() => expect(page()).toContain("工作区正被另一个 Tacet 使用"));
    expect(modal()?.type).toBe("unsavedDecisions");
    expect(fake.calls).not.toContain("publish");

    fake.decideFails = null;
    act(() => button("重新保存").click());
    await waitFor(() => expect(modal()).toBeNull());
    expect(fake.calls).not.toContain("publish"); // the file is redone first: exporting is the person's next step
    expect(fake.decisionLog).toEqual([{ job: "j1", ds: [{ id: 31, keep: false }] }]);

    await setRows(["ready_to_publish"], ["completed"]);
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(fake.publishedWith!.get(31)).toBe(false);
  });

  it("写入失败时的提示说明是哪个文件", async () => {
    await refusedOnReadyFile();
    expect(toastText()).toContain("a.txt");
    expect(toastText()).toContain("未能保存");
  });

  it("写入正常时导出流程不变：不弹窗，直接发布", async () => {
    await batchOf(AB, ["ready_to_publish"], ["completed"]);
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep");
    act(() => returnToBatch());
    await waitFor(() => expect(fake.decisionLog).toHaveLength(1));
    await setRows(["ready_to_publish"], ["completed"]);
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(modal()).toBeNull();
    expect(fake.publishedWith!.get(31)).toBe(true);
  });

  it("从对话框打开文件：显示的是没存下的选择，不改动直接离开也会重发", async () => {
    await refusedOnReadyFile();
    exportByHand();
    await settle();
    expect(fake.calls).not.toContain("publish");
    expect(modal()?.type).toBe("unsavedDecisions");
    fake.decideFails = null;
    act(() => button("打开文件").click());
    await waitFor(() => expect(single()?.from).toBe("batch"));
    expect(modal()).toBeNull();
    expect(shown(0)).toBe("redact"); // the kernel still says “keep”
    expect(toastText()).toContain("未能保存的选择");
    act(() => returnToBatch());
    await waitFor(() => expect(fake.decisionLog).toEqual([{ job: "j1", ds: [{ id: 31, keep: false }] }]));
    await setRows(["ready_to_publish"], ["completed"]);
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(fake.publishedWith!.get(31)).toBe(false);
  });

  it("别的批次里没存下的选择，不挡这个批次的导出", async () => {
    await refusedOnReadyFile();
    fake.decideFails = null;
    fake.nextRows = [fake.row(5, "preflighted"), fake.row(6, "preflighted")];
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
    act(() => store.update((d) => { d.batch!.autoPub = false; }));
    startBatch();
    await waitFor(() => expect(batch().phase).toBe("run"));
    fake.rows = [fake.row(5, "ready_to_publish"), fake.row(6, "completed")];
    await act(() => fake.emitBatch());
    await waitFor(() => expect(buttons("导出 1 份").length).toBe(1));
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(fake.publishedBatch).toBe("b0002");
    expect(modal()).toBeNull();
  });

  it("跳过了的文件不再挡导出：它不会被导出", async () => {
    await batchOf(AB, ["awaiting_review", { pending: 1 }], ["ready_to_publish"]);
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep");
    await refusedOnLeaving();
    expect(await skipLive("j1")).toBe(true);
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(modal()).toBeNull();
  });

  it("跳过时写入还在路上，之后才失败：同样不挡导出", async () => {
    await batchOf(AB, ["awaiting_review", { pending: 1 }], ["ready_to_publish"]);
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep");
    const [held, release] = gate();
    fake.decideGate = held;
    fake.decideFails = new Error("STORE_FAILED");
    act(() => returnToBatch());
    await waitFor(() => expect(decides()).toBe(1));
    expect(await skipLive("j1")).toBe(true);
    release();
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(modal()).toBeNull();
  });

  it("等写入时批次已离开屏幕：仍然拒绝并说明，但不提供打开文件，重新保存照常", async () => {
    await batchOf(AB, ["ready_to_publish"], ["completed"]);
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep");
    const [held, release] = gate();
    fake.decideGate = held;
    fake.decideFails = new Error("STORE_FAILED");
    act(() => returnToBatch());
    await waitFor(() => expect(decides()).toBe(1));
    exportByHand();
    fake.nextRows = [fake.row(5, "preflighted"), fake.row(6, "preflighted")];
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    release();
    await waitFor(() => expect(modal()?.type).toBe("unsavedDecisions"));
    expect(fake.calls).not.toContain("publish");
    expect(page()).toContain("a.txt");
    expect(buttons("打开文件")).toEqual([]);
    fake.decideGate = null;
    fake.decideFails = null;
    act(() => button("重新保存").click());
    await waitFor(() => expect(modal()).toBeNull());
    expect(fake.decisionLog).toEqual([{ job: "j1", ds: [{ id: 31, keep: true }] }]);
  });

  it("重新处理同样被拒：不说“已重新排队”，也不关掉说明原因的对话框", async () => {
    await batchOf(AB, ["ready_to_publish"], ["blocked", { detail: "EXPORT_FAILED" }]);
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep");
    await refusedOnLeaving();
    act(() => openLiveGroup("live-blocked"));
    act(() => button("重新处理").click());
    await waitFor(() => expect(modal()?.type).toBe("unsavedDecisions"));
    await settle();
    expect(modal()?.type).toBe("unsavedDecisions");
    expect(fake.retried).toEqual([]);
    expect(toastText()).not.toContain("已重新排队");
  });

  it("“保存并退出”重发没存下的选择；屏幕上更新的选择排在它后面", async () => {
    await refusedOnReadyFile();
    fake.decideFails = null;
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep"); // changed again on the file that is open: this one is the person's last word
    const quit = quitting();
    act(() => saveAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.decisionLog.map((x) => x.ds[0]!.keep)).toEqual([false, true]);
  });

  it("离开文件后才重发：“保存并退出”也把它交给内核", async () => {
    await refusedOnReadyFile();
    fake.decideFails = null;
    const quit = quitting();
    act(() => saveAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.decisionLog).toEqual([{ job: "j1", ds: [{ id: 31, keep: false }] }]);
  });

  it("任务已丢弃：它没存下的选择不再挡“保存并退出”", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 11, start: 3, end: 14 }])];
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    await fake.emit("awaiting_review", { pending: 1 });
    await waitFor(() => expect(single().phase).toBe("review"));
    choose(0, "keep");
    fake.decideFails = new Error("STORE_FAILED");
    fake.nextRows = [fake.row(5, "preflighted")]; // the kernel never gives two files one job
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    await waitFor(() => expect(fake.decisionLog).toEqual([]));
    await waitFor(() => expect(decides()).toBe(1));
    const quit = quitting();
    act(() => dismissToast());
    act(() => saveAndQuit()); // while the task is kept, what was refused on it does hold the window
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    expect(quit.n).toBe(0);
    const entry = getState().history.find((h) => h.park && h.k === "file")!;
    await discardSavedTask(entry.id);
    act(() => saveAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
  });

  it("内核一直拒绝：“保存并退出”留住窗口，“停止并清理”仍能退出", async () => {
    await refusedOnReadyFile();
    const quit = quitting();
    act(() => dismissToast());
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("停止并清理"));
    expect(quit.n).toBe(0);
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cancelled).toEqual(["b0001"]);
  });
});

describe("#35 先后点开几个文件，只有最后一次点击生效", () => {
  const reviewable: [string, object?][] = [["awaiting_review", { pending: 1 }], ["awaiting_review", { pending: 1 }]];

  it("先点 A 再点 B，A 后读完：屏幕停在 B，B 上的选择和撤销都在，并写入内核", async () => {
    await batchOf(AB, ...reviewable);
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    openBatchFile(0);
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    choose(0, "keep");
    const undo = getState().undo.length;
    expect(undo).toBeGreaterThan(0);

    release();
    await settle();
    expect(single().name).toBe("b.txt");
    expect(single().live!.job).toBe("j2");
    expect(shown(0)).toBe("keep");
    expect(getState().undo).toHaveLength(undo);

    act(() => returnToBatch());
    await waitFor(() => expect(fake.decisionLog).toEqual([{ job: "j2", ds: [{ id: 31, keep: true }] }]));
  });

  it("先点 A 再点 B，A 先读完：不闪到 A；读 B 时显示正在打开 B，打开后消失", async () => {
    await batchOf(AB, ...reviewable);
    const [held, release] = gate();
    fake.documentGateOf.set("j2", held);
    openBatchFile(0);
    openBatchFile(1);
    await settle();
    expect(getState().single?.name).toBeUndefined();
    expect(opening()).toContain("b.txt");
    release();
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    expect(opening()).toBeNull();
  });

  it("读取期间批次页显示正在打开哪个文件", async () => {
    await batchOf(AB, ...reviewable);
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    act(() => openBatchFile(0));
    expect(opening()).toContain("a.txt");
    release();
    await waitFor(() => expect(single()?.name).toBe("a.txt"));
  });

  it("晚到的 A 读取失败：不打扰已经打开的 B", async () => {
    await batchOf(AB, ...reviewable);
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    openBatchFile(0);
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    act(() => dismissToast());
    fake.documentFails = new Error("STORE_FAILED");
    release();
    await waitFor(() => expect(fake.documentFails).toBeNull());
    await settle();
    expect(toastText()).toBe("");
    expect(single().name).toBe("b.txt");
  });

  it("读取失败时加载状态也会结束，并说明原因", async () => {
    await batchOf(AB, ...reviewable);
    fake.documentFails = new Error("STORE_FAILED");
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    act(() => openBatchFile(0));
    expect(opening()).toContain("a.txt");
    release();
    await waitFor(() => expect(toastText()).not.toBe(""));
    await waitFor(() => expect(opening()).toBeNull());
    expect(getState().single).toBeNull();
  });

  it("晚到的 PDF 不再渲染页面，也不报预览不可用", async () => {
    await batchOf(["/d/a.pdf", "/d/b.txt"], ["awaiting_review", { pending: 1, format: "pdf" }], ["awaiting_review", { pending: 1 }]);
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    openBatchFile(0);
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    act(() => dismissToast());
    release();
    await settle();
    expect(fake.calls.filter((c) => c.startsWith("preview"))).toEqual([]);
    expect(toastText()).toBe("");
  });

  it("PDF 在渲染页面时点了 B：渲染晚到也不覆盖 B", async () => {
    await batchOf(["/d/a.pdf", "/d/b.txt"], ["awaiting_review", { pending: 1, format: "pdf" }], ["awaiting_review", { pending: 1 }]);
    const [held, release] = gate();
    const real = fake.pagePreview.bind(fake);
    let drawing = false;
    fake.pagePreview = async (job, p, dpi) => { drawing = true; await held; return real(job, p, dpi); };
    openBatchFile(0);
    await waitFor(() => expect(drawing).toBe(true)); // its text is read, its first page is being drawn
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    choose(0, "keep");
    release();
    await settle();
    expect(single().name).toBe("b.txt");
    expect(shown(0)).toBe("keep");
  });

  it("读 A 时点了要密码的文件：对话框留着，A 不会跳到屏幕上", async () => {
    await batchOf(AB, ["awaiting_review", { pending: 1 }], ["locked", { detail: "password_required" }]);
    expect(batch().groups.map((g) => g.id)).toContain("live-password");
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    openBatchFile(0);
    await settle();
    act(() => openLiveGroup("live-password"));
    expect(modal()?.type).toBe("livePassword");
    expect(opening()).toBeNull();
    release();
    await settle();
    expect(getState().single).toBeNull();
    expect(modal()?.type).toBe("livePassword");
  });

  it("屏幕上的文件被替换：它的选择先写入内核，等在后面的单文件任务还在", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14 }])];
    await batchOf(AB, ...reviewable);
    openBatchFile(0);
    await waitFor(() => expect(single()?.name).toBe("a.txt"));
    choose(0, "keep");
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    expect(getState().undo).toEqual([]);
    await waitFor(() => expect(fake.decisionLog).toEqual([{ job: "j1", ds: [{ id: 31, keep: true }] }]));
  });

  it("正常单次打开：加载状态消失，返回批次时没改过就不写", async () => {
    await batchOf(AB, ...reviewable);
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    act(() => returnToBatch());
    await settle();
    expect(fake.decisionLog).toEqual([]);
    expect(opening()).toBeNull();
  });
});
