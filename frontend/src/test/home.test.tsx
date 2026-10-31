import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "@testing-library/react";
import { $, $$, button, chooseFiles, click, go, mountApp, press, pick, menuTexts, tick, win } from "./helpers";

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
    press("查看全部");
    expect($(".history-modal")!.textContent).toContain("最近任务");
    expect($(".history-modal")!.textContent).toContain("3 个任务");
    press("关闭", ".history-modal");
    expect($(".history-modal")).toBeNull();
  });

  it("方案菜单可切换方案并包含“管理方案”", () => {
    click(".drop-preset");
    expect(menuTexts()).toContain("简历脱敏");
    expect(menuTexts()).toContain("管理方案");
    pick("日志清理");
    expect($(".drop-preset")!.textContent).toContain("日志清理");
  });

  it("“管理方案”打开方案页，返回回到首页", () => {
    click(".drop-preset");
    pick("管理方案");
    expect($("#tbTitle")!.textContent).toContain("方案");
    press("返回");
    expect($("#tbTitle")!.textContent).toContain("Tacet");
  });

  it("打开已导出的历史任务进入结果页并可查看清单", () => {
    click($$(".hrow").find((e) => e.textContent!.includes("述职报告"))!);
    expect($("#tbTitle")!.textContent).toContain("述职报告.docx");
    expect(win()).toContain("已导出");
    press("查看清单");
    expect($("#tbTitle")!.textContent).toContain("导出清单");
    expect(win()).toContain("SHA-256");
    press("返回");
    press("返回");
    expect($("#tbTitle")!.textContent).toContain("Tacet");
  });

  it("已导出的历史批次：结果页统计与清单", () => {
    click($$(".hrow").find((e) => e.textContent!.includes("季度客户材料"))!);
    expect(win()).toContain("已全部导出");
    press("查看清单");
    expect($$("tbody tr")).toHaveLength(48);
  });

  it("可恢复的批次：停止并清理", () => {
    click($$(".hrow").find((e) => e.textContent!.includes("供应商合同"))!);
    expect($("#layer .modal")!.textContent).toContain("继续后从中断处处理");
    press("停止并清理");
    expect($(".home-history")!.textContent).not.toContain("供应商合同");
    expect($(".toast")!.textContent).toContain("已清理");
  });

  it("选择多个文档创建批次预检，选择一个文档开始检测", () => {
    chooseFiles("a.docx", "b.pdf", "c.txt");
    expect($("#tbTitle")!.textContent).toContain("批次 · 3 份");
    press("取消");
    chooseFiles("合同.pdf");
    expect(win()).toContain("读取");
    tick(5000);
    expect(win()).toContain("确认并导出");
  });

  it("拖入文档到拖入区与到窗口其他位置都会接收", () => {
    const dropOn = (target: Element) => {
      const ev = new Event("drop", { bubbles: true, cancelable: true });
      Object.defineProperty(ev, "dataTransfer", { value: { files: [new File(["x"], "拖入.pdf")], types: ["Files"] } });
      act(() => { target.dispatchEvent(ev); });
    };
    dropOn($("#drop")!);
    expect(win()).toContain("拖入.pdf");
    go("home");
    dropOn($(".home-brand")!);
    expect(win()).toContain("拖入.pdf");
  });
});

describe("首次使用", () => {
  it("未下载模型时首页显示引导与标题栏入口", () => {
    go("fresh");
    expect(win()).toContain("请先下载隐私识别模型");
    expect($("#modelShell")!.hidden).toBe(false);
    press("前往下载");
    expect($("#tbTitle")!.textContent).toContain("插件");
    expect(button("下载模型")).toBeTruthy();
  });
});
