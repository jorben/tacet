import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { S } from "../engine/state";
import { $, $$, click, clickA, go, key, mountApp, tick, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("单份文档", () => {
  it("检测中显示读取→检测，并可停止", () => {
    go("s1");
    expect(win()).toContain("读取");
    expect(win()).toContain("检测");
    clickA("cancelSingle");
    expect($("#tbTitle")!.textContent).toContain("Tacet");
    expect($(".toast")!.textContent).toContain("已停止");
  });

  it("检测完成进入复核；缩略图、类别统计、待看列表齐全", () => {
    go("s1");
    tick(4000);
    expect($(".review")).toBeTruthy();
    expect($$(".thumb")).toHaveLength(3);
    expect(win()).toContain("命中");
    expect(win()).toContain("待看");
    expect(win()).toContain("PDF · 栅格化");
    expect($('[data-a="exportSingle"]')!.textContent).toContain("确认并导出");
  });

  it("命中气泡：保留 / 涂抹，同值一起，撤销", () => {
    go("s2");
    const n0 = S.single.hits.filter((h: any) => h.state === "redact").length;
    clickA("hit", "0");
    expect($("#layer .hp")).toBeTruthy();
    click($('#layer [data-a="setHit"][data-v="keep"]')!);
    expect(S.single.hits[0].state).toBe("keep");
    expect(S.single.hits.filter((h: any) => h.state === "redact").length).toBeLessThan(n0);
    clickA("undo");
    expect(S.single.hits[0].state).toBe("redact");
  });

  it("三种视图：原文 / 效果 / 对比，及 E、C 快捷键", () => {
    go("s2");
    clickA("mode", "fx");
    expect($(".doc.fx")).toBeTruthy();
    clickA("mode", "cmp");
    expect($(".review.cmp")).toBeTruthy();
    expect(win()).toContain("效果 · 预览");
    key("c");
    expect($(".review.cmp")).toBeNull();
    key("e");
    expect($(".doc.fx")).toBeTruthy();
  });

  it("类别处理方式：占位符 / 全部保留；图像菜单", () => {
    go("s2");
    clickA("catMenu", "name");
    click($$("#layer .mi").find((e) => e.textContent!.includes("占位符"))!);
    expect(S.single.catAct.name).toBe("ph");
    clickA("catMenu", "date");
    click($$("#layer .mi").find((e) => e.textContent!.includes("全部保留"))!);
    expect(S.single.hits.filter((h: any) => h.type === "date").every((h: any) => h.state === "keep")).toBe(true);
    clickA("imgAllMenu");
    click($$("#layer .mi").find((e) => e.textContent!.includes("保留全部"))!);
    expect(win()).toContain("部分结果");
    expect($('[data-a="exportSingle"]')!.textContent).toContain("导出部分结果");
  });

  it("DOCX 使用约页码与章节段落定位", () => {
    go("s5");
    expect(win()).toContain("DOCX · 保留版式");
    expect(win()).toContain("约 1 / 3");
    expect(win()).toMatch(/第[一二三四五六七八九]条 · 第 \d+ 段/);
  });

  it("切换方案前确认重新检测；快捷键面板；导出位置菜单", () => {
    go("s2");
    clickA("hit", "0");
    click($('#layer [data-a="setHit"][data-v="keep"]')!);
    clickA("presetMenu");
    click($$("#layer .mi").find((e) => e.textContent!.includes("简历脱敏"))!);
    expect($("#layer .modal")!.textContent).toContain("将按新方案重新检测");
    clickA("presetGo");
    expect(win()).toContain("读取");
    tick(4000);
    clickA("keys");
    expect($("#layer .keys")!.textContent).toContain("J / ↓");
    clickA("outMenu");
    click($$("#layer .mi").find((e) => e.textContent!.includes("与原文件相同位置"))!);
    expect(win()).toContain("原文件旁");
  });

  it("导出：导出→验证，完成页统计、清单、再处理一份", () => {
    go("s2");
    clickA("exportSingle");
    expect(win()).toContain("导出");
    expect(win()).toContain("验证");
    tick(3000);
    expect(win()).toContain("已导出");
    expect(win()).toContain("按策略处理，未声明零遗漏");
    clickA("singlePhase", "manifest");
    expect($("#tbTitle")!.textContent).toContain("导出清单");
    expect(win()).toContain("劳动合同_已脱敏.pdf");
    clickA("singlePhase", "result");
    clickA("go", "home");
    expect(win()).toContain("劳动合同.pdf");
  });

  it("同一类型任务占位：未导出的文件需选择保存/返回", () => {
    go("s2");
    clickA("go", "home");
    const input = $("#fileIn") as HTMLInputElement;
    Object.defineProperty(input, "files", { value: [new File(["x"], "新文件.pdf")], configurable: true });
    input.dispatchEvent(new Event("change", { bubbles: true }));
    expect($("#layer .modal")!.textContent).toContain("还未导出");
    clickA("slotPark");
    expect($(".toast")!.textContent).toContain("已保存到“最近”");
  });
});
