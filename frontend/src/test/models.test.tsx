import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "@testing-library/react";
import { store } from "../store";
import { $, $$, chooseFiles, go, mountApp, press, tick, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => vi.useFakeTimers());

describe("模型准备状态", () => {
  it("首次使用：待下载引导，进入插件页可见模型卡片", () => {
    go("fresh");
    expect(win()).toContain("请先下载隐私识别模型");
    press("前往下载");
    expect($('#modelPlugins [data-model="astr"]')).toBeTruthy();
    expect(win()).toContain("下载模型");
    expect(win()).toContain("权重约 36 MiB");
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
