import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent } from "@testing-library/react";
import { store } from "../store";
import { homeRelative, hubUrl, splitPath } from "../actions/models";
import { FakeEngine } from "./liveFake";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { $, $$, chooseFiles, go, mountApp, press, tick, win } from "./helpers";
import { getState } from "../store";

beforeAll(mountApp);
beforeEach(() => vi.useFakeTimers());

describe("模型准备状态", () => {
  it("首次使用：待下载引导，进入插件页可见模型卡片", () => {
    go("fresh");
    expect(win()).toContain("请先下载隐私识别模型");
    press("前往下载");
    expect($('#modelPlugins [data-model="astr"]')).toBeTruthy();
    expect(win()).toContain("下载模型");
    expect(win()).toContain("权重约 72 MiB");
  });

  it("插件页显示模型的推理设备与存储位置（内核报告时）", () => {
    go("model-plugins");
    expect($("#modelPlugins .model-runtime")).toBeNull();
    act(() => store.update((d) => { d.models[0]!.runtime = { dir: "/Users/x/Tacet/models/astrlink-guard-int8", backend: "CPUExecutionProvider", device: "cpu", repository: "", accelerable: false, accelerated: false }; }));
    const rt = $("#modelPlugins .model-runtime")!;
    expect(rt.textContent).toContain("推理设备CPU · ONNX Runtime");
    expect(rt.textContent).toContain("存储位置~/Tacet/models/astrlink-guard-int8");
    expect(rt.querySelector("dd")!.getAttribute("title")).toBe("CPUExecutionProvider");
    act(() => store.update((d) => { d.models[0]!.runtime!.device = "gpu"; d.models[0]!.runtime!.backend = "CoreMLExecutionProvider"; }));
    expect($("#modelPlugins .model-runtime")!.textContent).toContain("GPU · Core ML");
    act(() => store.update((d) => { delete d.models[0]!.runtime; }));
  });

  it("插件页模型卡的 HF 标签在浏览器中打开模型页；地址不合规时不显示", async () => {
    const opened: string[] = [];
    setService({ ...mockService, openUrl: async (u: string) => { opened.push(u); } });
    go("model-plugins");
    expect($("#modelPlugins .model-hf")).toBeNull();
    const rt = { dir: "/m/astrlink-guard-int8", backend: "CPUExecutionProvider", device: "cpu" as const, repository: "QuantumNous/astrlink-guard", accelerable: false, accelerated: false };
    act(() => store.update((d) => { d.models[0]!.runtime = rt; }));
    const hf = $("#modelPlugins .model-hf")!;
    expect(hf.textContent).toContain("HF");
    expect(hf.getAttribute("title")).toBe("在 Hugging Face 查看 QuantumNous/astrlink-guard");
    act(() => hf.click());
    expect(opened).toEqual(["https://huggingface.co/QuantumNous/astrlink-guard"]);
    for (const bad of ["", "astrlink-guard", "a/b/c", "../x", "https://evil.example/x", "a/b?x"]) {
      act(() => store.update((d) => { d.models[0]!.runtime = { ...rt, repository: bad }; }));
      expect($("#modelPlugins .model-hf")).toBeNull();
    }
    expect(hubUrl("a/..")).toBeNull();
    act(() => store.update((d) => { delete d.models[0]!.runtime; }));
    setService(null);
  });

  it("插件页：内存不足时下载按钮不可用并说明原因", () => {
    go("model-plugins");
    act(() => store.update((d) => {
      d.models.push({ ...d.models[0]!, id: "pf", name: "OpenAI Privacy Filter", languages: "英文为主", weightMiB: 1569, status: "missing", progress: 0, eligible: false, minMemoryGiB: 16 });
    }));
    const card = $('#modelPlugins [data-model="pf"]')!;
    expect(card.textContent).toContain("权重约 1.5 GiB");
    expect(card.textContent).toContain("需要至少 16 GiB 内存，本机不满足，无法下载");
    expect(card.querySelector<HTMLButtonElement>(".model-actions .btn")!.disabled).toBe(true);
    expect($$("#modelPlugins .model-list > .model-card")).toHaveLength(2);
    act(() => store.update((d) => { d.models.pop(); }));
  });

  it("插件页：模型卡片放在双栏容器里，格式等其他插件仍是单栏列表", () => {
    go("model-plugins");
    expect($("#modelPlugins > .model-list > .model-card")).toBeTruthy();
    expect($$(".model-list .row")).toHaveLength(0);
    expect($(".model-list")!.closest("#modelPlugins")).toBeTruthy();
  });

  it.each(["missing", "downloading", "paused", "verifying", "loading", "ready", "error"] as const)("插件页：模型在 %s 状态仍显示权重体积", (status) => {
    go("model-catalog");
    act(() => store.update((d) => {
      d.models.forEach((m) => { m.status = status; });
    }));
    expect($('#modelPlugins [data-model="astr"] .model-meta')!.textContent).toContain("权重约 72 MiB");
    expect($('#modelPlugins [data-model="pf"] .model-meta')!.textContent).toContain("权重约 2.6 GiB");
  });

  it("存储位置：路径分成可省略的前段与始终显示的末段；末尾的图标打开内核报告的目录", async () => {
    const long = "/Users/someone/Library/Application Support/Tacet/models/astrlink-guard-int8";
    expect(splitPath(long)).toEqual(["/Users/someone/Library/Application Support/Tacet/models", "/astrlink-guard-int8"]);
    expect(splitPath("C:\\Users\\x\\AppData\\Tacet\\m1")).toEqual(["C:\\Users\\x\\AppData\\Tacet", "\\m1"]);
    const tail = "/" + "x".repeat(50);
    const [h, t] = splitPath("/a" + tail);
    expect(h + t).toBe("/a" + tail);
    expect(t).toHaveLength(20);
    // a long last folder is cut after a separator, so the tail starts at a word
    expect(splitPath("~/m/installed/astrlink-guard_0.1.3_0a070d3113615e33")).toEqual(["~/m/installed/astrlink-guard_", "0.1.3_0a070d3113615e33"]);
    expect(homeRelative("/Users/jorben/Library/x")).toBe("~/Library/x");
    expect(homeRelative("/Users/jorben")).toBe("~");
    expect(homeRelative("/Volumes/Users/jorben/x")).toBe("/Volumes/Users/jorben/x");
    expect(splitPath("nofolder")).toEqual(["", "nofolder"]);
    const engine = new FakeEngine();
    setService({ ...mockService, engine });
    go("model-plugins");
    act(() => store.update((d) => {
      d.models[0]!.runtime = { dir: long, backend: "CPUExecutionProvider", device: "cpu", repository: "", accelerable: false, accelerated: false };
    }));
    const path = $("#modelPlugins .model-path")!;
    expect(path.querySelector(".model-path-head")!.textContent).toBe("~/Library/Application Support/Tacet/models");
    expect(path.querySelector(".model-path-tail")!.textContent).toBe("/astrlink-guard-int8");
    expect(path.querySelector(".model-path-text")!.getAttribute("title")).toBe(long);
    // what is copied is the real path, not the "~" shown
    const copied: string[] = [];
    fireEvent.copy(path.querySelector(".model-path-text")!, { clipboardData: { setData: (_: string, v: string) => copied.push(v) } });
    expect(copied).toEqual([long]);
    const open = path.querySelector<HTMLButtonElement>('[aria-label="打开存储位置"]')!;
    await act(async () => { open.click(); });
    expect(engine.calls).toContain("openModelDirectory:astr");
    engine.modelDirFails = new Error("OPEN_DIRECTORY_FAILED");
    await act(async () => { open.click(); });
    expect($(".toast")!.textContent).toContain("无法打开模型目录");
    act(() => store.update((d) => { delete d.models[0]!.runtime; }));
    setService(null);
  });

  it("GPU 加速默认开启且始终可见；关闭只切换设备，不改变模型", () => {
    go("settings");
    act(() => store.update((d) => { d.settings.gpu = true; }));
    const sw = () => $('#settingsPage [role="switch"][aria-label="GPU 加速"]')!;
    const before = getState().models.map((m) => [m.id, m.version, m.variant]);
    expect(sw().getAttribute("aria-checked")).toBe("true");
    expect($("#settingsPage")!.textContent).toContain("设备支持时所有模型优先使用 GPU");
    act(() => sw().click());
    expect(getState().settings.gpu).toBe(false);
    expect($("#settingsPage")!.textContent).toContain("所有模型使用 CPU 推理");
    expect(getState().models.map((m) => [m.id, m.version, m.variant])).toEqual(before);
    act(() => sw().click());
    expect(getState().settings.gpu).toBe(true);
  });

  it("插件页只有一份 FP16 模型；确认前显示 GPU 优先，检测后显示确认的 GPU，降级后显示 CPU", () => {
    go("model-catalog");
    expect($("#modelPlugins .model-accel")).toBeNull();
    expect($('#modelPlugins [data-model="astr"]')!.textContent).toContain("FP16");
    expect($('#modelPlugins [data-model="astr"]')!.textContent).toContain("GPU 优先 · 首次任务时确认");
    act(() => store.update((d) => { d.models[0]!.runtime!.detecting = true; }));
    expect($('#modelPlugins [data-model="astr"]')!.textContent).toContain("GPU 优先 · 正在检测");
    act(() => store.update((d) => { Object.assign(d.models[0]!.runtime!, { detecting: false, deviceCode: "PLUGIN_HANDSHAKE_TIMEOUT" }); }));
    expect($('#modelPlugins [data-model="astr"]')!.textContent).toContain("GPU 优先 · 检测未完成，首次任务时确认");
    expect($('#modelPlugins [data-model="astr"] .model-runtime dd[title="PLUGIN_HANDSHAKE_TIMEOUT"]')).not.toBeNull();
    act(() => store.update((d) => { delete d.models[0]!.runtime!.deviceCode; }));
    act(() => store.update((d) => { Object.assign(d.models[0]!.runtime!, { detecting: false, device: "gpu", backend: "CoreMLExecutionProvider", accelerated: true }); }));
    expect($('#modelPlugins [data-model="astr"]')!.textContent).toContain("GPU · Core ML");
    expect($('#modelPlugins [data-model="astr"]')!.textContent).not.toContain("GPU 优先");
    act(() => store.update((d) => { d.models[0]!.runtime!.device = "cpu"; d.models[0]!.runtime!.backend = "CPUExecutionProvider"; }));
    expect($('#modelPlugins [data-model="astr"]')!.textContent).toContain("CPU · ONNX Runtime");
    expect($('#modelPlugins [data-model="astr"]')!.textContent).toContain("FP16");
  });

  it("下载中 → 校验中 → 准备中 → 已就绪，可暂停", () => {
    go("model-plugins");
    press("下载模型");
    expect(win()).toContain("下载中");
    expect($("progress")).toBeTruthy();
    press("暂停");
    expect(win()).toContain("已暂停");
    expect(win()).toContain("继续下载");
    press("继续下载");
    tick(500 * 20);
    expect(win()).toContain("校验中");
    tick(500);
    expect(win()).toContain("准备中");
    tick(500);
    expect($(".toast")!.textContent).toContain("AstrLink Guard 已就绪");
    expect(win()).toContain("已就绪");
  });

  it.each([
    ["model-offline", "下载已中断", "继续下载"],
    ["model-disk", "可用空间不足", "检查空间并重试"],
    ["model-integrity", "模型文件校验未通过", "重新下载"],
    ["model-load", "模型未能启动", "重新准备"],
  ])("错误状态 %s 显示原因与操作", (k, title, action) => {
    go(k);
    expect(win()).toContain("模型准备需要处理");
    press("查看并处理");
    expect(win()).toContain(title);
    expect(win()).toContain(action);
    expect(win()).toContain("需处理");
  });

  it("已暂停：首页提示继续准备", () => {
    go("model-paused");
    expect(win()).toContain("模型下载已暂停，已下载的部分会保留。");
    expect(win()).toContain("继续准备");
  });

  it("加载与试运行：正在准备本地识别，完成后就绪", () => {
    go("model-loading");
    press("查看进度");
    expect(win()).toContain("准备中");
    tick(900);
    expect(win()).toContain("已就绪");
  });

  it("已选文件等待模型：取消本次操作 / 就绪后继续处理", () => {
    go("model-wait");
    expect(win()).toContain("尚未开始检测");
    expect(($$("button").find((b) => b.textContent === "继续处理") as HTMLButtonElement).disabled).toBe(true);
    press("取消本次操作");
    expect(win()).not.toContain("尚未开始检测");
    go("model-wait");
    press("下载模型");
    tick(500 * 22 + 1500);
    expect(($$("button").find((b) => b.textContent === "继续处理") as HTMLButtonElement).disabled).toBe(false);
    press("继续处理");
    expect(win()).toContain("读取");
  });

  it("等待中再选文件：提示先继续或取消", () => {
    go("model-wait");
    chooseFiles("另一个.pdf");
    expect($(".toast")!.textContent).toContain("请先继续或取消已选择的操作");
  });

  it("批量：模型未就绪时预检显示卡片与“准备模型后处理”", () => {
    go("model-batch");
    expect(win()).toContain("此方案需要的模型尚未下载");
    expect(win()).toContain("准备模型后处理");
    press("准备模型后处理");
    expect(win()).toContain("取消本次操作");
    expect((($$("button").find((b) => b.textContent === "准备模型后处理")) as HTMLButtonElement).disabled).toBe(true);
  });

  it("文档处理中不允许下载模型", () => {
    go("s1");
    act(() => store.update((d) => { d.models[0]!.status = "missing"; }));
    press("插件", "#rail");
    press("下载模型");
    expect($(".toast")!.textContent).toContain("请先完成或停止当前文档处理");
  });

  it("标题栏入口只在模型待下载时出现，网络状态随下载变化", () => {
    go("fresh");
    expect(($("#modelShell") as HTMLElement).hidden).toBe(false);
    expect($("#networkState")!.getAttribute("aria-label")).toBe("本机处理 · 未联网");
    press("前往下载");
    press("下载模型");
    expect($("#networkState")!.getAttribute("aria-label")).toBe("正在下载模型 · 文档留在本机");
    expect($$("#modelShell")).toHaveLength(1);
  });

  it("重置模型状态会停掉进行中的下载计时器", () => {
    go("model-download");
    go("home");
    tick(500 * 40);
    expect(store.get().models[0]!.status).toBe("ready");
  });
});
