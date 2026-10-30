import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { $, $$, click, clickA, go, mountApp, tick, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => {
  vi.useFakeTimers();
  go("home");
});

describe("首页与最近任务（场景夹具：原型预置的最近任务）", () => {
  it("展示拖入区、方案选择与最近的 3 条任务", () => {
    expect(win()).toContain("拖入文档");
    expect(win()).toContain("DOCX");
    expect($(".drop-preset")!.textContent).toContain("通用办公脱敏");
    expect($$(".home-history .hrow")).toHaveLength(3);
    expect(win()).toContain("已保存 · 6 天后清理");
  });

  it("“查看全部”打开最近任务对话框并可关闭", () => {
    clickA("allHistory");
    expect($(".history-modal")!.textContent).toContain("最近任务");
    expect($(".history-modal")!.textContent).toContain("3 个任务");
    clickA("closeModal");
    expect($(".history-modal")).toBeNull();
  });

  it("方案菜单可切换方案并包含“管理方案”", () => {
    clickA("homePreset");
    const items = $$("#layer .mi").map((e) => e.textContent);
    expect(items.join()).toContain("简历脱敏");
    expect(items.join()).toContain("管理方案");
    click($$("#layer .mi").find((e) => e.textContent!.includes("日志清理"))!);
    expect($(".drop-preset")!.textContent).toContain("日志清理");
  });

  it("打开已导出的历史任务进入结果页并可查看清单", () => {
    clickA("openHist", "1");
    expect($("#tbTitle")!.textContent).toContain("述职报告.docx");
    expect(win()).toContain("已导出");
    clickA("singlePhase", "manifest");
    expect($("#tbTitle")!.textContent).toContain("导出清单");
    expect(win()).toContain("SHA-256");
    clickA("go", "home");
  });

  it("可恢复的批次：继续处理 / 停止并清理", () => {
    click($$(".hrow").find((e) => e.textContent!.includes("供应商合同"))!);
    expect($("#layer .modal")!.textContent).toContain("继续后从中断处处理");
    clickA("histClean");
    expect($(".home-history")!.textContent).not.toContain("供应商合同");
  });

  it("选择多个文档创建批次预检，选择一个文档开始检测", () => {
    const input = $("#fileIn") as HTMLInputElement;
    const files = ["a.docx", "b.pdf", "c.txt"].map((n) => new File(["x"], n));
    Object.defineProperty(input, "files", { value: files, configurable: true });
    input.dispatchEvent(new Event("change", { bubbles: true }));
    expect($("#tbTitle")!.textContent).toContain("批次 · 3 份");
    clickA("cancelBatch");
    Object.defineProperty(input, "files", { value: [new File(["x"], "合同.pdf")], configurable: true });
    input.dispatchEvent(new Event("change", { bubbles: true }));
    expect(win()).toContain("读取");
    tick(5000);
    expect(win()).toContain("确认并导出");
  });
});

describe("首次使用", () => {
  it("未下载模型时首页显示引导与标题栏入口", () => {
    go("fresh");
    expect(win()).toContain("请先下载隐私识别模型");
    expect($("#modelShell")!.hidden).toBe(false);
    clickA("modelManage");
    expect($("#tbTitle")!.textContent).toContain("插件");
  });
});
