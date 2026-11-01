import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "@testing-library/react";
import { getState } from "../store";
import { $, $$, button, chooseFiles, click, go, key, menuTexts, mountApp, pick, press, tick, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => vi.useFakeTimers());

const single = () => getState().single!;
const redacted = () => single().hits.filter((h) => h.state === "redact").length;

describe("单份文档", () => {
  it("检测中显示读取→检测，并可停止", () => {
    go("s1");
    expect(win()).toContain("读取");
    expect(win()).toContain("检测");
    press("停止");
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
    expect(button("确认并导出").textContent).toContain("确认并导出");
  });

  it("命中气泡：保留 / 涂抹，同值一起，撤销", () => {
    go("s2");
    const n0 = redacted();
    click("#h0");
    expect($("#layer .hp")).toBeTruthy();
    press("保留", "#layer .hp");
    expect(single().hits[0]!.state).toBe("keep");
    expect(redacted()).toBeLessThan(n0);
    press("撤销");
    expect(single().hits[0]!.state).toBe("redact");
    expect(redacted()).toBe(n0);
  });

  it("同值的命中一起处理，可取消勾选只改一处", () => {
    go("s2");
    const twin = single().hits.find((h) => single().hits.filter((x) => x.text === h.text && x.type === h.type).length > 1)!;
    click("#h" + twin.id);
    expect($("#layer .hp-same")!.textContent).toMatch(/同值 \d 处一起/);
    press("保留", "#layer .hp");
    expect(single().hits.filter((h) => h.text === twin.text).every((h) => h.state === "keep")).toBe(true);
    press("撤销");
    click("#h" + twin.id);
    click("#sameAll");
    press("保留", "#layer .hp");
    expect(single().hits.filter((h) => h.text === twin.text && h.state === "keep")).toHaveLength(1);
  });

  it("三种视图：原文 / 效果 / 对比，及 E、C 快捷键", () => {
    go("s2");
    press("效果", ".doc-tools");
    expect($(".doc.fx")).toBeTruthy();
    press("对比", ".doc-tools");
    expect($(".review.cmp")).toBeTruthy();
    expect(win()).toContain("效果 · 预览");
    key("c");
    expect($(".review.cmp")).toBeNull();
    key("e");
    expect($(".doc.fx")).toBeTruthy();
  });

  it("对比视图中，悬停一侧的命中会标出另一侧的同一处", () => {
    go("s4");
    const hit = $$(".cmp-row .hit")[0]!;
    act(() => { hit.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); });
    expect($$(".hit.twin").length).toBe(2);
    act(() => { $(".doc")!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); });
    expect($$(".hit.twin").length).toBe(0);
  });

  it("键盘：J/K 在命中间移动，X 切换保留，⌘Z 撤销", () => {
    go("s2");
    key("j");
    tick(300);
    const id = single().focus;
    expect(id).toBeGreaterThanOrEqual(0);
    key("x");
    expect(single().hits[id]!.state).toBe("keep");
    key("z", { metaKey: true });
    expect(single().hits[id]!.state).toBe("redact");
    key("k");
    expect(single().focus).not.toBe(id);
  });

  it("类别处理方式：占位符 / 全部保留；图像菜单", () => {
    go("s2");
    press("人名 处理方式");
    pick("占位符");
    expect(single().catAct.name).toBe("ph");
    press("日期 处理方式");
    pick("全部保留");
    expect(single().hits.filter((h) => h.type === "date").every((h) => h.state === "keep")).toBe(true);
    press("图像 2", ".sum");
    pick("保留全部");
    expect(win()).toContain("部分结果");
    expect(button("导出部分结果")).toBeTruthy();
  });

  it("点击文档中的图像可单独选择涂抹或保留", () => {
    go("s2");
    click(".d-photo");
    expect(menuTexts()).toContain("保留");
    pick("保留");
    expect(single().imgs[0]!.act).toBe("keep");
    press("撤销");
    expect(single().imgs[0]!.act).toBe("remove");
  });

  it("DOCX 使用约页码与章节段落定位", () => {
    go("s5");
    expect(win()).toContain("DOCX · 保留版式");
    expect(win()).toContain("约 1 / 3");
    expect(win()).toMatch(/第[一二三四五六七八九]条 · 第 \d+ 段/);
  });

  it("选中文字补充涂抹：选择类别后新增一处人工命中，可撤销", () => {
    go("s2");
    const run = $$("#docScroll .tx").find((e) => e.textContent!.includes("本合同为固定期限"))!;
    const range = document.createRange();
    range.setStart(run.firstChild!, 2);
    range.setEnd(run.firstChild!, 8);
    range.getBoundingClientRect = () => new DOMRect(10, 10, 40, 16);
    const sel = window.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
    act(() => { $("#docScroll")!.dispatchEvent(new MouseEvent("mouseup", { bubbles: true })); });
    expect($("#layer .typegrid")).toBeTruthy();
    const n = single().hits.length;
    press("电话", "#layer .typegrid");
    expect(single().hits).toHaveLength(n + 1);
    expect(single().hits.at(-1)!.manual).toBe(true);
    expect($(".toast")!.textContent).toContain("已涂抹 · 电话");
    press("撤销", ".toast");
    expect(single().hits).toHaveLength(n);
  });

  it("切换方案前确认重新检测；快捷键面板；导出位置菜单", () => {
    go("s2");
    click("#h0");
    press("保留", "#layer .hp");
    press("办公外发", ".sum");
    pick("简历脱敏");
    expect($("#layer .modal")!.textContent).toContain("将按新方案重新检测");
    press("切换并重新检测");
    expect(win()).toContain("读取");
    tick(4000);
    press("快捷键");
    expect($("#layer .keys")!.textContent).toContain("J / ↓");
    press("导出位置");
    pick("与原文件相同位置");
    expect(win()).toContain("原文件旁");
  });

  it("没有修改时切换方案直接重新检测", () => {
    go("s2");
    press("办公外发", ".sum");
    pick("日志清理");
    expect($("#layer .modal")).toBeNull();
    expect(win()).toContain("读取");
    expect($(".toast")!.textContent).toContain("已切换 · 重新检测");
  });

  it("导出：导出→验证，完成页统计、清单、再处理一份", () => {
    go("s2");
    press("确认并导出");
    expect(win()).toContain("导出");
    expect(win()).toContain("验证");
    tick(3000);
    expect(win()).toContain("已导出");
    expect(win()).toContain("按策略处理，未声明零遗漏");
    press("查看清单");
    expect($("#tbTitle")!.textContent).toContain("导出清单");
    expect(win()).toContain("劳动合同_已脱敏.pdf");
    press("返回");
    press("再处理一份");
    expect(win()).toContain("劳动合同.pdf");
  });

  it("同一类型任务占位：未导出的文件需选择保存/返回", () => {
    go("s2");
    press("返回");
    chooseFiles("新文件.pdf");
    expect($("#layer .modal")!.textContent).toContain("还未导出");
    press("保存并继续");
    expect($(".toast")!.textContent).toContain("已保存到“最近”");
    tick(5000);
    press("返回");
    expect($(".home-history")!.textContent).toContain("劳动合同.pdf");
  });

  it("处理中的文件占着位置：再选文件只提示等待", () => {
    go("s1");
    press("返回");
    chooseFiles("另一份.pdf");
    expect($(".toast")!.textContent).toContain("正在处理，请稍候");
    expect($("#layer .modal")).toBeNull();
  });

  it("停止后旧的计时器不会再改动状态", () => {
    go("s1");
    press("停止");
    tick(5000);
    expect(getState().single).toBeNull();
  });
});
