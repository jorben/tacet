// #34: a write of choices the kernel refused stands in the way of anything that goes on from them, until it lands.
// #35: of the files of a batch clicked one after another, only the last click changes the screen.
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { openBatchFile, startBatch } from "../actions/batch";
import { discardSavedTask } from "../actions/home";
import { openLiveGroup, publishLiveBatch, skipLive, startLive, takePaths } from "../actions/live";
import { goTo, openSubpage } from "../actions/navigation";
import { focusHit, returnToBatch, setHitState } from "../actions/single";
import { requestQuit, saveAndQuit, stopAndQuit } from "../actions/window";
import { closeModal, dismissToast, openModal, overlayStore } from "../overlay/store";
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
  fake.rows = rowsOf(...paths.map((): [string] => ["preflighted"]));
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
async function refusedOnReadyFile(second = "completed") {
  await batchOf(AB, ["ready_to_publish"], [second]);
  await fake.decide("j1", [{ id: 31, keep: true }]);
  await setRows(["ready_to_publish"], [second]);
  fake.decisionLog = [];
  fake.calls = [];
  openBatchFile(0);
  await waitFor(() => expect(single()?.from).toBe("batch"));
  expect(shown(0)).toBe("keep");
  choose(0, "redact");
  await refusedOnLeaving();
}

/** What the kernel was told about one file, in the order it landed. */
const landed = (job: string) => fake.decisionLog.filter((x) => x.job === job).map((x) => x.ds[0]!.keep);

/** Opens a file of the batch, makes a choice and leaves it. The write is on its way until the returned call lets it land. */
async function heldWrite(fi: number, state: "keep" | "redact") {
  const job = "j" + (fi + 1);
  openBatchFile(fi);
  await waitFor(() => expect(single()?.live?.job).toBe(job));
  choose(0, state);
  const [held, release] = gate();
  fake.decideGateOf.set(job, held);
  const before = decides();
  act(() => returnToBatch());
  await waitFor(() => expect(decides()).toBe(before + 1));
  return release;
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

  it("“保存并退出”：文件开在屏幕上时只发屏幕上的选择，没存下的旧选择不再发", async () => {
    await refusedOnReadyFile();
    fake.decideFails = null;
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep"); // changed again on the file that is open: this one is the person's last word
    const quit = quitting();
    act(() => saveAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(landed("j1")).toEqual([true]);
  });

  it("屏幕上的文件写入失败：“保存并退出”只发一次，不重发，并说明", async () => {
    await refusedOnReadyFile();
    openBatchFile(0);
    await waitFor(() => expect(single()?.from).toBe("batch"));
    choose(0, "keep");
    const before = decides();
    const quit = quitting();
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("窗口未关闭"));
    expect(decides()).toBe(before + 1);
    expect(quit.n).toBe(0);
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
    const before = decides();
    act(() => saveAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(decides()).toBe(before); // nothing is sent to a task that is gone
  });

  it("内核一直拒绝：“保存并退出”留住窗口，“停止并清理”仍能退出", async () => {
    await refusedOnReadyFile();
    const quit = quitting();
    act(() => dismissToast());
    act(() => saveAndQuit());
    await waitFor(() => expect(toastText()).toContain("停止并清理"));
    // the person is told how far the way out reaches before choosing it; what it costs is the file (#41, stopScope.test.tsx)
    expect(toastText()).toContain("只清理「a.txt」");
    expect(toastText()).toContain("已导出的文件不受影响");
    expect(quit.n).toBe(0);
    act(() => requestQuit());
    expect(page()).toContain("只清理这些文件的中间数据");
    act(() => stopAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cleanedFiles).toEqual(["j1"]);
    expect(fake.cancelled).toEqual([]);
  });
});

describe("等写入的时候，人还在继续操作", () => {
  const bothReady: [string, object?][] = [["ready_to_publish"], ["ready_to_publish"]];

  it("别的文件写入在路上时“保存并退出”：屏幕上的选择立刻发出，等待期间再改的排在它后面", async () => {
    await batchOf(AB, ...bothReady);
    const release = await heldWrite(1, "keep");
    openBatchFile(0);
    await waitFor(() => expect(single()?.live?.job).toBe("j1"));
    choose(0, "keep");
    const quit = quitting();
    act(() => saveAndQuit());
    choose(0, "redact"); // the window is still there, and the person has changed their mind
    act(() => returnToBatch());
    await settle();
    expect(quit.n).toBe(0);
    release();
    await waitFor(() => expect(quit.n).toBe(1));
    expect(landed("j1")).toEqual([true, false]);
  });

  it("“保存并退出”等待期间又改了没存下的文件：旧选择不重发，盖不住新写入", async () => {
    await refusedOnReadyFile("ready_to_publish");
    fake.decideFails = null;
    const releaseOther = await heldWrite(1, "redact");
    const quit = quitting();
    act(() => saveAndQuit());
    const releaseNewer = await heldWrite(0, "keep");
    releaseOther();
    await settle();
    expect(quit.n).toBe(0); // the newer write is still on its way
    releaseNewer();
    await waitFor(() => expect(quit.n).toBe(1));
    expect(landed("j1")).toEqual([true]);
  });

  // Exporting cannot be waited on like this any more: no file opens during its wait (#42, below). Reprocessing still can.
  const twoReadyOneBlocked: [string, object?][] = [["ready_to_publish"], ["ready_to_publish"], ["blocked", { detail: "EXPORT_FAILED" }]];
  const reprocess = () => {
    act(() => openLiveGroup("live-blocked"));
    act(() => button("重新处理").click());
  };

  it("W1 在路上时点重新处理，等待期间发出 W2：W1 结束也不重试，等 W2 落地", async () => {
    await batchOf([...AB, "/d/c.txt"], ...twoReadyOneBlocked);
    const releaseFirst = await heldWrite(0, "keep");
    reprocess();
    fake.units = [unit(1, "电话 13812345678", [{ id: 32, start: 3, end: 14 }])];
    const releaseSecond = await heldWrite(1, "keep");
    releaseFirst();
    await settle();
    expect(fake.retried).toEqual([]);
    releaseSecond();
    await waitFor(() => expect(fake.retried).not.toEqual([]));
    expect(fake.kernelDecisions.get(31)).toBe(true);
    expect(fake.kernelDecisions.get(32)).toBe(true);
  });

  it("W1 在路上时点重新处理，等待期间发出的 W2 失败：同样拦住，说明是哪个文件", async () => {
    await batchOf([...AB, "/d/c.txt"], ...twoReadyOneBlocked);
    const releaseFirst = await heldWrite(0, "keep");
    reprocess();
    const releaseSecond = await heldWrite(1, "keep");
    releaseFirst();
    await settle();
    fake.decideFails = new Error("STORE_FAILED");
    releaseSecond();
    await waitFor(() => expect(modal()?.type).toBe("unsavedDecisions"));
    expect(page()).toContain("b.txt");
    expect(fake.retried).toEqual([]);
  });
});

describe("#42 点了导出之后，选择不会再悄悄落空", () => {
  const bothReady: [string, object?][] = [["ready_to_publish"], ["ready_to_publish"]];
  const TOO_LATE = "这次修改不会生效";

  /** The kernel holds “keep” on the second file, as in the issue: changing it to “redact” is what must not go missing. */
  async function secondFileKept() {
    await batchOf(AB, ...bothReady);
    await fake.decide("j2", [{ id: 31, keep: true }]);
    await setRows(...bothReady);
    fake.decisionLog = [];
    fake.calls = [];
  }
  /** The kernel has started on the files; the screen has not heard yet. */
  const kernelAhead = (...states: [string, object?][]) => { fake.rows = rowsOf(...states); };

  it("写入在路上时点导出：等待期间打不开别的文件，也就改不了选择；写入落地后发布", async () => {
    await secondFileKept();
    const release = await heldWrite(0, "keep");
    exportByHand(2);
    openBatchFile(1);
    await settle();
    expect(getState().single).toBeNull();
    expect(getState().view).toBe("batch");
    expect(toastText()).toContain("正在导出");
    expect(fake.calls).not.toContain("publish");

    release();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(fake.publishedWith!.get(31)).toBe(true);
    await waitFor(() => expect(toastText()).toContain("已开始导出"));

    // the wait is over: the file opens, and what happens to a choice on it now is the kernel's answer
    openBatchFile(1);
    await waitFor(() => expect(single()?.live?.job).toBe("j2"));
  });

  it("文件还在读取时点导出：读完也不打开，屏幕不会跳进一份来不及改的文件", async () => {
    await secondFileKept();
    const [read, finishRead] = gate();
    fake.documentGateOf.set("j2", read);
    openBatchFile(1);
    await waitFor(() => expect(opening()).toContain("b.txt"));
    exportByHand(2);
    await waitFor(() => expect(fake.published).toBeTruthy());
    finishRead();
    await settle();
    expect(getState().single).toBeNull();
    expect(getState().view).toBe("batch");
    expect(opening()).toBeNull();
  });

  it("等待结束后不留锁：发布被拒，文件照常打开", async () => {
    await secondFileKept();
    fake.publishFails = new Error("STORE_FAILED");
    exportByHand(2);
    await waitFor(() => expect(fake.calls).toContain("publish"));
    await settle();
    openBatchFile(1);
    await waitFor(() => expect(single()?.live?.job).toBe("j2"));
  });

  it("别的批次的导出在等待，不挡这个批次打开文件", async () => {
    await secondFileKept();
    const release = await heldWrite(0, "keep");
    exportByHand(2);
    fake.nextRows = [fake.row(5, "ready_to_publish"), fake.row(6, "ready_to_publish")];
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    await waitFor(() => expect(batch().files[0]?.st).toBe("ready"));
    openBatchFile(0);
    await waitFor(() => expect(single()?.name).toBe("c.txt"));
    release();
  });

  it("等待期间再点一次导出：只发布一次", async () => {
    await secondFileKept();
    const release = await heldWrite(0, "keep");
    exportByHand(2);
    const again = publishLiveBatch(); // not awaited before the release: a second publish would wait for that same write
    release();
    await act(() => again);
    await waitFor(() => expect(fake.published).toBeTruthy());
    await settle();
    expect(fake.calls.filter((c) => c === "publish")).toEqual(["publish"]);
  });

  it("屏幕还没听到发布开始时改了选择：离开时提示未能保存，从不说已保存，内核里还是旧选择", async () => {
    await secondFileKept();
    openBatchFile(1);
    await waitFor(() => expect(single()?.live?.job).toBe("j2"));
    expect(shown(0)).toBe("keep");
    choose(0, "redact");
    kernelAhead(["publishing"], ["publishing"]);
    act(() => dismissToast());
    const [held, release] = gate();
    fake.decideGateOf.set("j2", held);
    act(() => returnToBatch());
    await settle();
    expect(toastText()).not.toContain("已保存"); // said now, it would be a guess

    release();
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    expect(toastText()).toContain("b.txt");
    expect(toastText()).toContain(TOO_LATE);
    expect(fake.kernelDecisions.get(31)).toBe(true);
  });

  it("写入被收下才提示已保存修改；因别的原因被拒同样不说已保存", async () => {
    await secondFileKept();
    openBatchFile(1);
    await waitFor(() => expect(single()?.live?.job).toBe("j2"));
    choose(0, "redact");
    const [held, release] = gate();
    fake.decideGateOf.set("j2", held);
    act(() => returnToBatch());
    await settle();
    expect(toastText()).not.toContain("已保存");
    release();
    await waitFor(() => expect(toastText()).toContain("已保存修改"));

    await setRows(...bothReady);
    act(() => dismissToast());
    openBatchFile(1);
    await waitFor(() => expect(single()?.live?.job).toBe("j2"));
    choose(0, "keep");
    fake.decideFails = new Error("STORE_FAILED");
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    await settle();
    expect(toastText()).not.toContain("已保存");
  });

  /** A choice on the second file arrived too late; the first file is still to be exported. */
  async function refusedTooLate() {
    await secondFileKept();
    openBatchFile(1);
    await waitFor(() => expect(single()?.live?.job).toBe("j2"));
    choose(0, "redact");
    kernelAhead(["ready_to_publish"], ["publishing"]);
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain(TOO_LATE));
    await setRows(["ready_to_publish"], ["completed"]);
  }

  it("太晚的选择不挡别的文件导出：它不可能再存下", async () => {
    await refusedTooLate();
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(modal()).toBeNull();
  });

  it("太晚的选择不挡“保存并退出”：它不可能再存下，不能把人关在窗口里", async () => {
    await refusedTooLate();
    const quit = quitting();
    act(() => saveAndQuit());
    await waitFor(() => expect(quit.n).toBe(1));
  });

  it("文件退回可处理后再打开：显示内核里的选择，不说是上次没存下的；再改能写入", async () => {
    await refusedTooLate();
    await setRows(...bothReady);
    act(() => dismissToast());
    openBatchFile(1);
    await waitFor(() => expect(single()?.live?.job).toBe("j2"));
    expect(shown(0)).toBe("keep");
    expect(toastText()).not.toContain("上次未能保存");
    choose(0, "redact");
    act(() => returnToBatch());
    await waitFor(() => expect(fake.kernelDecisions.get(31)).toBe(false));
    await waitFor(() => expect(toastText()).toContain("已保存修改"));
  });

  it("重新保存时有写入在路上：等它落地，旧选择不盖新选择", async () => {
    await refusedOnReadyFile("ready_to_publish");
    expect(fake.kernelDecisions.get(31)).toBe(true);
    fake.decideFails = null;
    // the person went back to the file and changed their mind again: this write is newer than what was refused
    const release = await heldWrite(0, "keep");
    act(() => openModal({ type: "unsavedDecisions", targets: [{ job: "j1", name: "a.txt", reason: "STORE_FAILED" }] }));
    act(() => button("重新保存").click());
    await settle();
    expect(modal()?.type).toBe("unsavedDecisions"); // the answer is not known yet
    release();
    await waitFor(() => expect(modal()).toBeNull());
    await settle();
    expect(landed("j1")).toEqual([true]);
    expect(fake.kernelDecisions.get(31)).toBe(true);
  });

  it("重新保存时文件已开始导出：不说已保存，说明原因；关掉后不再挡导出", async () => {
    await refusedOnReadyFile("ready_to_publish");
    exportByHand(2);
    await waitFor(() => expect(modal()?.type).toBe("unsavedDecisions"));
    fake.decideFails = null;
    kernelAhead(["publishing"], ["ready_to_publish"]);
    act(() => button("重新保存").click());
    await waitFor(() => expect(page()).toContain(TOO_LATE));
    await settle();
    expect(modal()?.type).toBe("unsavedDecisions");
    expect(toastText()).not.toContain("选择已保存");
    expect(fake.kernelDecisions.get(31)).toBe(true);

    act(() => button("取消").click());
    await setRows(["completed"], ["ready_to_publish"]);
    exportByHand();
    await waitFor(() => expect(fake.published).toBeTruthy());
    expect(modal()).toBeNull();
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

  it("PDF 渲染页面失败、没有换文件：提示预览不可用，改为文本", async () => {
    await batchOf(["/d/a.pdf", "/d/b.txt"], ["awaiting_review", { pending: 1, format: "pdf" }], ["awaiting_review", { pending: 1 }]);
    fake.previewFails = new Error("STORE_FAILED");
    openBatchFile(0);
    await waitFor(() => expect(single()?.name).toBe("a.pdf"));
    expect(toastText()).toContain("预览不可用");
  });

  it("PDF 在渲染页面时点了 B，渲染随后失败：B 上不提示预览不可用", async () => {
    await batchOf(["/d/a.pdf", "/d/b.txt"], ["awaiting_review", { pending: 1, format: "pdf" }], ["awaiting_review", { pending: 1 }]);
    const [held, release] = gate();
    let drawing = false;
    fake.pagePreview = async () => { drawing = true; await held; throw new Error("STORE_FAILED"); };
    openBatchFile(0);
    await waitFor(() => expect(drawing).toBe(true));
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    act(() => dismissToast());
    release();
    await settle();
    expect(toastText()).toBe("");
    expect(single().name).toBe("b.txt");
  });

  it("单文件 PDF 在渲染页面时拖入新文件，渲染随后失败：新任务上不提示预览不可用", async () => {
    fake.units = [unit(1, "x", [{ id: 11, start: 0, end: 1 }])];
    takePaths(["/d/a.pdf"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    const [held, release] = gate();
    let drawing = false;
    fake.pagePreview = async () => { drawing = true; await held; throw new Error("STORE_FAILED"); };
    await fake.emit("awaiting_review", { pending: 1, format: "pdf" });
    await waitFor(() => expect(drawing).toBe(true));
    fake.nextRows = [fake.row(5, "preflighted")];
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    act(() => dismissToast());
    release();
    await settle();
    expect(toastText()).toBe("");
    expect(single().name).toBe("b.txt");
  });

  it("单文件的长文档读取时拖入新文件：新任务上不提示文档较长", async () => {
    fake.units = Array.from({ length: 3000 }, (_, i) => unit(i + 1, "x", []));
    takePaths(["/d/a.txt"]);
    await waitFor(() => expect(fake.calls).toContain("start"));
    const [held, release] = gate();
    fake.documentGate = held;
    await fake.emit("awaiting_review", { pending: 1 });
    fake.nextRows = [fake.row(5, "preflighted")];
    takePaths(["/d/b.txt"]);
    await waitFor(() => expect(single().name).toBe("b.txt"));
    act(() => dismissToast());
    release();
    await settle();
    expect(toastText()).toBe("");
  });

  it("读 A 时批次被替换：A 不打开，也不提示没存下的选择；新批次页不显示正在打开 A", async () => {
    await refusedOnReadyFile();
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    act(() => openBatchFile(0));
    expect(opening()).toContain("a.txt");
    fake.decideFails = null;
    fake.nextRows = [fake.row(5, "preflighted"), fake.row(6, "preflighted")];
    takePaths(["/d/c.txt", "/d/d.txt"]);
    await waitFor(() => expect(batch().live!.id).toBe("b0002"));
    expect(getState().fileOpening?.name).toBe("a.txt"); // the read is still on: only the page must not say so
    await settle();
    expect(opening()).toBeNull();
    act(() => dismissToast());
    release();
    await settle();
    expect(getState().single).toBeNull();
    expect(toastText()).toBe("");
    expect(getState().fileOpening).toBeNull();
  });

  it.each([
    ["顶层页面", () => goTo("settings"), "settings"],
    ["子页面", () => openSubpage("presets"), "presets"],
  ] as const)("点 A 后切到%s，A 晚到：屏幕不跳回 A，也不留加载状态", async (_, leave, view) => {
    await batchOf(AB, ...reviewable);
    const [held, release] = gate();
    fake.documentGateOf.set("j1", held);
    openBatchFile(0);
    await waitFor(() => expect(getState().fileOpening?.name).toBe("a.txt"));
    act(() => leave());
    release();
    await settle();
    expect(getState().view).toBe(view);
    expect(getState().single).toBeNull();
    expect(getState().fileOpening).toBeNull();
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

  it("屏幕上的文件被替换：它的选择先写入内核，等在后面的单文件任务和它的撤销记录还在", async () => {
    await batchOf(AB, ...reviewable);
    fake.nextRows = [fake.row(5, "preflighted")]; // the kernel never gives two files one job
    takePaths(["/d/c.txt"]);
    await waitFor(() => expect(single()?.name).toBe("c.txt"));
    await waitFor(() => expect(fake.begunCount()).toBe(2));
    fake.rows = [fake.row(5, "awaiting_review", { pending: 1 })];
    await act(() => fake.emitBatch());
    await waitFor(() => expect(single().phase).toBe("review"));
    choose(0, "keep");
    const undo = getState().undo.length;
    expect(undo).toBeGreaterThan(0);
    act(() => goTo("batch"));

    openBatchFile(0);
    await waitFor(() => expect(single()?.name).toBe("a.txt"));
    choose(0, "keep");
    openBatchFile(1);
    await waitFor(() => expect(single()?.name).toBe("b.txt"));
    expect(getState().undo).toEqual([]);
    await waitFor(() => expect(fake.decisionLog).toEqual([{ job: "j1", ds: [{ id: 31, keep: true }] }]));
    expect(getState().behind?.single.name).toBe("c.txt");
    expect(getState().behind?.undo).toHaveLength(undo);

    act(() => returnToBatch());
    await waitFor(() => expect(single().name).toBe("c.txt"));
    expect(shown(0)).toBe("keep");
    expect(getState().undo).toHaveLength(undo);
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
