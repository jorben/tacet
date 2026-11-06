// #42, the review of PR #46: leaving must not be how a choice goes missing. Once the kernel has started on a file it
// takes no more choices on it, and the person hears of that when it happens, and at the latest before the window goes.
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { act, waitFor } from "@testing-library/react";
import { openBatchFile, startBatch } from "../actions/batch";
import { refusalOf, refusedJobs } from "../actions/decisions";
import { publishLiveBatch, retryLive, startLive, takePaths } from "../actions/live";
import { addManualRedaction, clickHit, focusHit, openAllImagesMenu, openCategoryMenu, openImageMenu, returnToBatch, setHitState, toggleFocusedHit, undoSingle } from "../actions/single";
import { requestQuit } from "../actions/window";
import { closeModal, closePop, dismissToast, overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { getState, resetStore, store } from "../store";
import { setSelectionPopover } from "../features/single/selection";
import { FakeEngine, unit } from "./liveFake";
import { button, buttons, mountApp, pick } from "./helpers";

beforeAll(mountApp);

let fake: FakeEngine;
let stop: () => void;
let quit: { n: number };

beforeEach(() => {
  act(() => { resetStore(); closeModal(); closePop(); dismissToast(); });
  fake = new FakeEngine();
  quit = { n: 0 };
  setService({ ...mockService, engine: fake, host: "darwin", quitApp: async () => { quit.n++; } } as never);
  stop = startLive();
});
afterEach(() => {
  stop();
  setService(null);
});

const NOT_IN_OUTPUT = "不会进入导出文件";
const single = () => getState().single!;
const batch = () => getState().batch!;
const modal = () => overlayStore.get().modal;
const toastText = () => overlayStore.get().toast?.msg ?? "";
const page = () => document.body.textContent!.replace(/\s+/g, " ");
const settle = () => new Promise((r) => setTimeout(r, 20));
const decides = () => fake.calls.filter((c) => c === "decide").length;
const rowsOf = (...states: string[]) => states.map((state, i) => fake.row(i, state));
const choose = (i: number, state: "keep" | "redact") => act(() => { focusHit(i); setHitState(state); });
const gate = () => { let open = () => {}; const p = new Promise<void>((r) => { open = r; }); return [p, open] as const; };

/** Two files ready to publish, not authorized, the kernel holding “keep” on the second. */
async function ready() {
  fake.units = [unit(1, "电话 13812345678", [{ id: 31, start: 3, end: 14 }])];
  fake.rows = rowsOf("preflighted", "preflighted");
  takePaths(["/d/a.txt", "/d/b.txt"]);
  await waitFor(() => expect(batch()?.live?.id).toBe("b0001"));
  await waitFor(() => expect(batch().files[0]?.st).not.toBe("proc"));
  act(() => store.update((d) => { d.batch!.autoPub = false; }));
  startBatch();
  await waitFor(() => expect(batch().phase).toBe("run"));
  await fake.decide("j2", [{ id: 31, keep: true }]);
  await setRows("ready_to_publish", "ready_to_publish");
  fake.decisionLog = [];
  fake.calls = [];
}
async function setRows(...states: string[]) {
  fake.rows = rowsOf(...states);
  await act(() => fake.emitBatch());
}
/** The kernel has moved on; the screen has not heard, as for at least 150ms after a publish began. */
const kernelAhead = (...states: string[]) => { fake.rows = rowsOf(...states); };

async function openSecond() {
  act(() => openBatchFile(1));
  await waitFor(() => expect(single()?.live?.job).toBe("j2"));
  expect(single().hits[0]!.state).toBe("keep");
}

/** “关闭窗口”, as the person does it, up to “保存并关闭”. */
function saveAndCloseByHand() {
  act(() => requestQuit());
  expect(modal()?.type).toBe("closeWindow");
  act(() => button("保存并关闭").click());
}

describe("保存并关闭遇到 DECISIONS_CLOSED：先说一次，再点才退出", () => {
  it("文件开着时内核已开始发布，屏幕还没听到：改成涂抹后保存并关闭，提示且不关窗；内核仍是保留；再点才退出", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    kernelAhead("publishing", "publishing");
    act(() => dismissToast());
    saveAndCloseByHand();
    await waitFor(() => expect(decides()).toBe(1));
    await waitFor(() => expect(toastText()).toContain(NOT_IN_OUTPUT));
    expect(toastText()).toContain("b.txt");
    await settle();
    expect(quit.n).toBe(0);
    expect(fake.kernelDecisions.get(31)).toBe(true);
    expect(single().live!.closed).toBe(true); // the kernel said so: the screen does not go on as if it could change

    saveAndCloseByHand();
    await waitFor(() => expect(quit.n).toBe(1));
  });

  it("只说一次：离开时已提示过的太晚，不再拦窗口", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    kernelAhead("ready_to_publish", "publishing");
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    await setRows("ready_to_publish", "publishing");
    saveAndCloseByHand();
    await waitFor(() => expect(quit.n).toBe(1));
  });

  it("屏幕外存不下的选择，重发时才知道太晚：同样先说，不直接退出", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    fake.decideFails = new Error("STORE_FAILED");
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    fake.decideFails = null;
    kernelAhead("ready_to_publish", "publishing");
    act(() => dismissToast());
    saveAndCloseByHand();
    await waitFor(() => expect(toastText()).toContain(NOT_IN_OUTPUT));
    expect(toastText()).toContain("b.txt");
    await settle();
    expect(quit.n).toBe(0);
    saveAndCloseByHand();
    await waitFor(() => expect(quit.n).toBe(1));
  });

  it("同时有写入失败和太晚：提示两件事都说，之后写入成功就能退出，不再为太晚拦一次", async () => {
    await ready();
    fake.decideFailsOf.set("j1", new Error("STORE_FAILED"));
    act(() => openBatchFile(0));
    await waitFor(() => expect(single()?.live?.job).toBe("j1"));
    choose(0, "keep");
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    await openSecond();
    choose(0, "redact");
    kernelAhead("ready_to_publish", "publishing");
    saveAndCloseByHand();
    await waitFor(() => expect(toastText()).toContain("窗口未关闭"));
    expect(toastText()).toContain("STORE_FAILED");
    expect(toastText()).toContain("「b.txt」");
    expect(toastText()).toContain(NOT_IN_OUTPUT);
    expect(quit.n).toBe(0);
    expect(single().live!.closed).toBe(true);

    fake.decideFailsOf.clear();
    saveAndCloseByHand();
    await waitFor(() => expect(quit.n).toBe(1));
  });

  it("“停止并清理”遇到太晚：先说，不停也不清理；再点才退出", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    kernelAhead("publishing", "publishing");
    act(() => requestQuit());
    act(() => button("停止并清理").click());
    await waitFor(() => expect(toastText()).toContain(NOT_IN_OUTPUT));
    await settle();
    expect(quit.n).toBe(0);
    expect(fake.cancelled).toEqual([]);

    act(() => requestQuit());
    act(() => button("停止并清理").click());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.cancelled).toContain("b0001");
  });

  it("“停止并清理”只清写入失败的文件时遇到太晚：点名的照清，另一个文件先说再退出", async () => {
    await ready();
    fake.decideFailsOf.set("j1", new Error("STORE_FAILED"));
    act(() => openBatchFile(0));
    await waitFor(() => expect(single()?.live?.job).toBe("j1"));
    choose(0, "keep");
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    await openSecond();
    choose(0, "redact");
    kernelAhead("ready_to_publish", "publishing");
    act(() => requestQuit());
    act(() => button("停止并清理").click());
    await waitFor(() => expect(toastText()).toContain(NOT_IN_OUTPUT));
    expect(toastText()).toContain("b.txt");
    expect(fake.cleanedFiles).toEqual(["j1"]);
    await settle();
    expect(quit.n).toBe(0);
  });
});

describe("打开的文件进入发布或已完成：立刻提示并转只读", () => {
  it("文件在后台完成，人还开着它：立刻提示；有未发送的选择时不直接退出，对话框点名；确认后才退", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    act(() => dismissToast());
    await setRows("completed", "completed");
    expect(toastText()).toContain("b.txt");
    expect(toastText()).toContain(NOT_IN_OUTPUT);
    expect(single().live!.closed).toBe(true);
    expect(page()).toContain("现在只读");

    act(() => requestQuit());
    expect(quit.n).toBe(0);
    expect(modal()?.type).toBe("closeWindow");
    const dialog = document.querySelector("#layer .modal")!.textContent!;
    expect(dialog).toContain("有未保存的选择");
    expect(dialog).toContain("这里的修改不会进入导出文件");
    expect(dialog).toContain("b.txt");

    act(() => button("保存并关闭").click());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.kernelDecisions.get(31)).toBe(true);
  });

  it("进入发布时同样立刻提示；没有改动时不说修改落空", async () => {
    await ready();
    await openSecond();
    act(() => dismissToast());
    await setRows("ready_to_publish", "publishing");
    expect(toastText()).toContain("b.txt");
    expect(toastText()).toContain("现在只读");
    expect(toastText()).not.toContain(NOT_IN_OUTPUT);
    expect(single().live!.closed).toBe(true);
  });

  it.each(["publishing", "completed", "partial"])("文件进入 %s 都转只读，开着的弹层一并收起", async (state) => {
    await ready();
    await openSecond();
    act(() => clickHit(0, false));
    expect(overlayStore.get().pop).not.toBeNull();
    await setRows("ready_to_publish", state);
    expect(single().live!.closed).toBe(true);
    expect(overlayStore.get().pop).toBeNull();
  });

  it("图像的选择同样改不了", async () => {
    await ready();
    await openSecond();
    act(() => store.update((d) => { d.single!.imgs = [{ label: "图 1", act: "remove", p: 0, b: 0 } as never]; }));
    await setRows("ready_to_publish", "publishing");
    act(() => dismissToast());
    act(() => openImageMenu(document.body, 0));
    pick("保留");
    expect(single().imgs[0]!.act).toBe("remove");
    expect(toastText()).toContain("现在只读");
    act(() => openAllImagesMenu(document.body));
    pick("保留全部");
    expect(single().imgs[0]!.act).toBe("remove");
  });

  it("只读之后被暂停：状态说不清文档出去没有，仍然只读", async () => {
    await ready();
    await openSecond();
    await setRows("ready_to_publish", "publishing");
    await setRows("ready_to_publish", "paused");
    expect(single().live!.closed).toBe(true);
  });

  it("不拦过头：文件已完成但没有改动，窗口照常直接退出", async () => {
    await ready();
    await openSecond();
    await setRows("completed", "completed");
    act(() => requestQuit());
    await waitFor(() => expect(quit.n).toBe(1));
    expect(modal()).toBeNull();
  });

  it("不拦过头：别的文件进入发布，开着的文件照常可改，也不提示", async () => {
    await ready();
    await openSecond();
    act(() => dismissToast());
    await setRows("publishing", "ready_to_publish");
    expect(single().live!.closed).toBeFalsy();
    expect(toastText()).toBe("");
    choose(0, "redact");
    expect(single().hits[0]!.state).toBe("redact");
  });

  it("只读时每个改选择的入口都不起作用，也不发写入；看文档、回批次照常", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    await setRows("publishing", "publishing");
    const undoBefore = getState().undo.length;
    const tried = () => {
      expect(single().hits[0]!.state).toBe("redact");
      expect(getState().undo.length).toBe(undoBefore);
      expect(toastText()).toContain("现在只读");
    };
    act(() => dismissToast());
    act(() => { focusHit(0); setHitState("keep"); });
    tried();
    act(() => dismissToast());
    act(() => toggleFocusedHit());
    tried();
    act(() => dismissToast());
    act(() => undoSingle());
    tried();
    act(() => dismissToast());
    act(() => addManualRedaction("phone", { k: "0.0.0", st: 0, en: 2, text: "电话" }));
    tried();
    expect(fake.manual).toBeNull();
    act(() => dismissToast());
    expect(document.querySelector<HTMLButtonElement>('button[aria-label$="处理方式"]')!.disabled).toBe(true);
    act(() => openCategoryMenu(document.body, "phone"));
    pick("全部保留");
    tried();
    expect(document.querySelector<HTMLButtonElement>('button[aria-label="撤销"]')!.disabled).toBe(true);

    expect(decides()).toBe(0);
    act(() => button("返回批次").click());
    await waitFor(() => expect(getState().view).toBe("batch"));
  });

  it("只读时不再出现改选择的控件；同样的操作在可写时会出现，作为对照", async () => {
    await ready();
    fake.units = [unit(1, "电话 13812345678 13812345678", [{ id: 31, start: 3, end: 14 }, { id: 32, start: 15, end: 26 }])];
    await openSecond();
    const selectFirstWord = () => {
      const range = document.createRange();
      range.selectNodeContents(document.querySelector(".tx")!.firstChild!);
      range.setStart(document.querySelector(".tx")!.firstChild!, 0);
      range.setEnd(document.querySelector(".tx")!.firstChild!, 1);
      range.getBoundingClientRect = () => new DOMRect();
      window.getSelection()!.removeAllRanges();
      window.getSelection()!.addRange(range);
      act(() => setSelectionPopover());
    };
    const hitChoice = () => [...document.querySelectorAll("#layer .hp button")].filter((b) => ["保留", "涂抹"].includes(b.textContent!)).length;
    const together = () => document.querySelectorAll("#layer .hp #sameAll").length;

    selectFirstWord();
    expect(overlayStore.get().pop).not.toBeNull();
    act(() => closePop());
    act(() => clickHit(0, false));
    expect(hitChoice()).toBe(2);
    expect(together()).toBe(1);
    act(() => closePop());

    await setRows("ready_to_publish", "publishing");
    selectFirstWord();
    expect(overlayStore.get().pop).toBeNull();
    act(() => clickHit(0, false));
    expect(overlayStore.get().pop).not.toBeNull();
    expect(hitChoice()).toBe(0);
    expect(together()).toBe(0);
    expect(document.querySelector("#layer .hp")!.textContent).toContain("只读");
    window.getSelection()!.removeAllRanges();
    act(() => closePop());
  });

  it("读文档期间文件开始发布：不打开，说明原因", async () => {
    await ready();
    const [held, release] = gate();
    fake.documentGateOf.set("j2", held);
    act(() => openBatchFile(1));
    await waitFor(() => expect(getState().fileOpening?.name).toBe("b.txt"));
    await setRows("ready_to_publish", "publishing");
    release();
    await waitFor(() => expect(getState().fileOpening).toBeNull());
    expect(getState().single).toBeNull();
    expect(toastText()).toContain("现在只读");
  });

  it("单文件：确认并导出的正常路径不提示只读；复核时内核已在发布才转只读", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 51, start: 3, end: 14 }])];
    fake.rows = [fake.row(0, "awaiting_review", { id: "q1", pending: 1 })];
    takePaths(["/d/q.txt"]);
    await waitFor(() => expect(single()?.phase).toBe("review"));
    choose(0, "keep");
    act(() => dismissToast());
    await act(() => { fake.rows = [fake.row(0, "publishing", { id: "q1" })]; return fake.emitBatch(); });
    expect(single().live!.closed).toBe(true);
    expect(toastText()).toContain(NOT_IN_OUTPUT);
    expect(page()).toContain("现在只读");
    expect(buttons("确认并导出")).toEqual([]);
  });

  it("单文件：导出时听到发布不算太晚，不提示", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 51, start: 3, end: 14 }])];
    fake.rows = [fake.row(0, "awaiting_review", { id: "q1", pending: 1 })];
    takePaths(["/d/q.txt"]);
    await waitFor(() => expect(single()?.phase).toBe("review"));
    act(() => store.update((d) => { d.single!.phase = "exporting"; }));
    act(() => dismissToast());
    await act(() => { fake.rows = [fake.row(0, "publishing", { id: "q1" })]; return fake.emitBatch(); });
    expect(single().live!.closed).toBeFalsy();
    expect(toastText()).toBe("");
  });
});

describe("文件退回可写：太晚的记录不再是太晚", () => {
  /** A choice refused as too late on the second file, which then stands where `after` says. */
  async function refusedTooLate(...after: string[]) {
    await ready();
    await openSecond();
    choose(0, "redact");
    kernelAhead("ready_to_publish", "publishing");
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain("这次修改不会生效"));
    await setRows("ready_to_publish", "publishing");
    await setRows(...after);
    act(() => dismissToast());
    fake.calls = [];
  }

  it("发布失败、文件变成失败：重试不按旧选择继续，说明原因；重新保存后内核才是最后的选择，再重试才走", async () => {
    await refusedTooLate("ready_to_publish", "failed");
    await act(async () => { await retryLive(1); });
    expect(fake.calls).not.toContain("retry");
    expect(modal()?.type).toBe("unsavedDecisions");
    expect(page()).toContain("现在已退回");
    expect(fake.kernelDecisions.get(31)).toBe(true);

    act(() => button("重新保存").click());
    await waitFor(() => expect(modal()).toBeNull());
    expect(fake.kernelDecisions.get(31)).toBe(false);
    await act(async () => { await retryLive(1); });
    expect(fake.calls).toContain("retry");
    const order = fake.calls.filter((c) => c === "decide" || c === "retry");
    expect(order).toEqual(["decide", "retry"]);
  });

  it("验证未过、文件退回可导出：导出同样先拦，不交付旧选择", async () => {
    await refusedTooLate("ready_to_publish", "ready_to_publish");
    await act(async () => { await publishLiveBatch(); });
    expect(fake.calls).not.toContain("publish");
    expect(modal()?.type).toBe("unsavedDecisions");
  });

  it("保存并退出时把它再交给内核，内核收下才退", async () => {
    await refusedTooLate("ready_to_publish", "failed");
    saveAndCloseByHand();
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.kernelDecisions.get(31)).toBe(false);
  });

  it.each(["awaiting_review", "ready_to_publish", "failed", "blocked"])("文件回到 %s：可以再写入，太晚的记录按写入失败处理", async (state) => {
    await refusedTooLate("ready_to_publish", state);
    expect(refusedJobs()).toEqual(["j2"]);
  });

  it("文件回到失败时，真正的写入失败原样保留，原因不被改写", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    fake.decideFails = new Error("STORE_FAILED");
    act(() => returnToBatch());
    await waitFor(() => expect(toastText()).toContain("未能保存"));
    await setRows("ready_to_publish", "failed");
    expect(String(refusalOf("j2"))).toContain("STORE_FAILED");
  });

  it("“最近”里的单文件退回可写：太晚的记录同样复位，退出时再交给内核", async () => {
    fake.units = [unit(1, "电话 13812345678", [{ id: 51, start: 3, end: 14 }])];
    fake.rows = [fake.row(0, "awaiting_review", { id: "q1", pending: 1 })];
    takePaths(["/d/q1.txt"]);
    await waitFor(() => expect(single()?.phase).toBe("review"));
    const first = single().live!.batch;
    choose(0, "keep");
    fake.decideFailsOf.set("q1", new Error("DECISIONS_CLOSED"));
    fake.nextRows = [fake.row(0, "awaiting_review", { id: "q2", pending: 1 })];
    takePaths(["/d/q2.txt"]); // the first goes to “最近”, its choices to the kernel, which refuses them as too late
    await waitFor(() => expect(single()?.name).toBe("q2.txt"));
    await waitFor(() => expect(String(refusalOf("q1"))).toContain("DECISIONS_CLOSED"));
    await waitFor(() => expect(single()?.phase).toBe("review"));

    fake.decideFailsOf.clear();
    await act(() => fake.emitFor(first, [fake.row(0, "failed", { id: "q1" })]));
    expect(refusedJobs()).toEqual(["q1"]);
    saveAndCloseByHand();
    await waitFor(() => expect(quit.n).toBe(1));
    expect(fake.decisionsHeld("q1")).toEqual({ 51: true });
  });

  it("开着的文件退回：解除只读，屏幕上的选择还在，离开时写入内核", async () => {
    await ready();
    await openSecond();
    choose(0, "redact");
    await setRows("ready_to_publish", "publishing");
    expect(single().live!.closed).toBe(true);
    await setRows("ready_to_publish", "failed");
    expect(single().live!.closed).toBe(false);
    expect(toastText()).toContain("可以继续修改");
    expect(single().hits[0]!.state).toBe("redact");
    act(() => returnToBatch());
    await waitFor(() => expect(fake.kernelDecisions.get(31)).toBe(false));
    await waitFor(() => expect(toastText()).toContain("已保存修改"));
  });

  it.each(["publishing", "completed", "paused"])("不拦过头：文件 %s 时不复位，不挡别的文件导出", async (state) => {
    await refusedTooLate("ready_to_publish", state);
    await act(async () => { await publishLiveBatch(); });
    expect(fake.calls).toContain("publish");
    expect(modal()).toBeNull();
  });
});
