import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "@testing-library/react";
import { getState } from "../store";
import { timers } from "../actions/timers";
import { openBatchFile } from "../actions/batch";
import { $, $$, button, click, go, key, menuTexts, mountApp, pick, press, tick, type as typeInto, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => vi.useFakeTimers());

const batch = () => getState().batch!;

describe("批量 · 预检", () => {
  it("显示份数、格式统计、无法处理弹层与全部设置项", () => {
    go("b1");
    expect($("#tbTitle")!.textContent).toContain("批次 · 214 份");
    expect(win()).toContain("DOCX 111");
    expect(win()).toContain("无法处理 3");
    for (const t of ["方式", "方案", "图像与嵌入", "导出位置", "验证后自动导出", "中断后可恢复", "开始处理"]) expect(win()).toContain(t);
    press("无法处理 3");
    expect($("#layer .pop")!.textContent).toContain("无法处理 · 不会导出");
    expect($("#layer .pop")!.textContent).toMatch(/格式不支持|文件已损坏|超过 2,000 页/);
  });

  it("方式切换：引导 / 无人值守（自动导出锁定）/ 严格", () => {
    go("b1");
    press("无人值守");
    expect(button("无人值守").getAttribute("aria-pressed")).toBe("true");
    expect(button("验证后自动导出").hasAttribute("disabled")).toBe(true);
    press("严格");
    expect(button("验证后自动导出").hasAttribute("disabled")).toBe(false);
    press("引导");
  });

  it("预检菜单：方案、图像、导出位置、保留天数；自动导出开关", () => {
    go("b1");
    const rowButton = (title: string) => $$(".row").find((r) => r.querySelector(".rl")?.textContent === title)!.querySelector<HTMLElement>("button.sel")!;
    click(rowButton("方案"));
    expect(menuTexts()).toContain("简历脱敏");
    pick("日志清理");
    expect(rowButton("方案").textContent).toContain("日志清理");
    expect(win()).toContain("仅规则 · 无需模型");
    click(rowButton("图像与嵌入"));
    pick("保留");
    expect(rowButton("图像与嵌入").textContent).toContain("保留 · 部分结果");
    click(rowButton("中断后可恢复"));
    pick("30");
    expect(rowButton("中断后可恢复").textContent).toContain("保留 30 天");
    const before = button("验证后自动导出").getAttribute("aria-checked");
    press("验证后自动导出");
    expect(button("验证后自动导出").getAttribute("aria-checked")).not.toBe(before);
  });

  it("取消回到首页", () => {
    go("b1");
    press("取消");
    expect($("#tbTitle")!.textContent).toContain("Tacet");
  });

  it("相同的文件名列表得到相同的批次（演示数据可复现）", () => {
    go("b1");
    const first = JSON.stringify(batch().files.map((f) => [f.name, f.pages, f.st, f.h]));
    go("b1");
    expect(JSON.stringify(batch().files.map((f) => [f.name, f.pages, f.st, f.h]))).toBe(first);
  });
});

describe("批量 · 处理中", () => {
  it("进度、标签页与例外分组；暂停与继续", () => {
    go("b2");
    expect($(".bhead .big")!.textContent).toMatch(/\d+\s*\/\s*214/);
    expect(win()).toContain("待你处理");
    expect(win()).toContain("开始复核");
    for (const g of ["扫描页，无文本层", "截图中含文字", "疑似身份证号 · 校验位不符", "嵌入文件"]) expect(win()).toContain(g);
    press("暂停");
    expect($(".toast")!.textContent).toContain("已暂停派发");
    press("继续");
    expect($(".toast")!.textContent).toContain("已继续");
  });

  it("运行一段时间后进度前进", () => {
    go("b2");
    const before = batch().files.filter((f) => f.st === "done" || f.st === "partial").length;
    tick(240 * 40);
    expect(batch().files.filter((f) => f.st === "done" || f.st === "partial").length).toBeGreaterThan(before);
  });

  it("自动处理标签页列出类别统计并可抽查", () => {
    go("b2");
    press("自动处理", "[role=tablist]");
    expect(win()).toContain("公司名");
    expect(win()).toContain("图像");
    click($$(".row").find((r) => r.textContent!.includes("电话"))!.querySelector("button.btn")!);
    expect($("#layer .drawer")!.textContent).toContain("抽查 · 电话");
    expect($$("#layer .drow")).toHaveLength(8);
    press("换一批");
    expect($$("#layer .drow")).toHaveLength(8);
    press("完成", "#layer .drawer");
    expect($("#layer .drawer")).toBeNull();
  });

  it("抽查：点击脱敏值短暂显示，随后恢复", () => {
    go("b2");
    press("自动处理", "[role=tablist]");
    click($$(".row").find((r) => r.textContent!.includes("电话"))!.querySelector("button.btn")!);
    const mask = $("#layer .mask")!;
    const masked = mask.textContent!;
    expect(masked).toContain("•");
    click(mask);
    expect($("#layer .mask")!.textContent).not.toContain("•");
    tick(2600);
    expect($("#layer .mask")!.textContent).toBe(masked);
  });

  it("文件标签页：分页、不渲染全量成员表、搜索与筛选", () => {
    go("b2");
    press("文件", "[role=tablist]");
    tick(10);
    expect($$("tbody tr").length).toBe(60);
    expect($('button.link')!.textContent).toContain("再显示 60");
    press("再显示 60");
    expect($$("tbody tr").length).toBe(120);
    press("未导出", ".ftools");
    expect($$("tbody tr").length).toBeLessThanOrEqual(10);
    press("全部", ".ftools");
    typeInto(".search input", "T0007");
    expect($$("tbody tr")).toHaveLength(1);
    expect(win()).toContain("T0007");
    typeInto(".search input", "不存在的文件");
    expect(win()).toContain("没有匹配的文件");
  });

  it("整组处理菜单：保留需确认，可先去复核", () => {
    go("b2");
    const row = $$(".group.go").find((g) => g.textContent!.includes("截图中含文字"))!;
    click(row.querySelector("button.ib")!);
    pick("全部保留");
    expect($("#layer .modal")!.textContent).toContain("全部保留");
    expect($("#layer .modal")!.textContent).toContain("部分结果");
    press("先去复核");
    expect(batch().phase).toBe("review");
  });

  it("整组处理：直接涂抹，提示可撤销", () => {
    go("b2");
    const row = $$(".group.go").find((g) => g.textContent!.includes("嵌入文件"))!;
    const before = batch().groups.find((g) => g.id === "embed")!.done.length;
    click(row.querySelector("button.ib")!);
    pick("全部涂抹");
    expect(batch().groups.find((g) => g.id === "embed")!.done).toHaveLength(before + 1);
    expect(win()).toContain("已处理");
    press("撤销", ".toast");
    expect(batch().groups.find((g) => g.id === "embed")!.done).toHaveLength(before);
    expect($(".toast")!.textContent).toContain("已撤销");
  });

  it("停止：保留以便恢复 / 停止并清理", () => {
    go("b2");
    press("停止");
    expect(menuTexts()).toContain("停止并保留以便恢复");
    pick("停止并清理");
    expect($("#tbTitle")!.textContent).toContain("Tacet");
    expect($(".toast")!.textContent).toMatch(/已停止并清理「.+」，其他任务保留/);
    expect(getState().batch).toBeNull();
    go("b2");
    press("停止");
    pick("停止并保留以便恢复");
    expect($(".home-history")!.textContent).toContain("已保存 · 7 天后清理");
  });
});

describe("批量 · 人工复核", () => {
  it("队列、决策面板、本组其余一起、撤销、上一条/下一条与键盘", () => {
    go("b5");
    expect($("#tbTitle")!.textContent).toContain("人工复核");
    expect($("#rvq")).toBeTruthy();
    expect(win()).toContain("请决策");
    expect(win()).toContain("需要密码");
    const before = $(".rvp-f")!.textContent;
    key("ArrowDown");
    key("ArrowUp");
    press("下一条");
    press("上一条");
    // 选择一个非密码分组条目做决策
    click($$(".rvi").find((e) => e.textContent!.includes("第") && !e.textContent!.includes("已加密"))!);
    const rv = batch().rv!;
    const cur = rv.items.find((x) => x.key === rv.cur)!;
    click($$(".dopt")[0]!);
    expect($(".rvp-f")!.textContent).not.toBe(before);
    expect(batch().rv!.items.find((x) => x.key === cur.key)!.dec).toBe(batch().groups[cur.gi]!.acts[0]!.k);
    expect($(".toast")).toBeTruthy();
    key("z", { metaKey: true });
    expect($(".toast")!.textContent).toContain("已撤销");
    expect(batch().rv!.items.find((x) => x.key === cur.key)!.dec).toBeNull();
  });

  it("A / B / 1 / 2 键做决策", () => {
    go("b5");
    click($$(".rvi").find((e) => e.textContent!.includes("第") && !e.textContent!.includes("已加密"))!);
    const rv0 = batch().rv!;
    const cur = rv0.items.find((x) => x.key === rv0.cur)!;
    key("b");
    const g = batch().groups[cur.gi]!;
    expect(batch().rv!.items.find((x) => x.key === cur.key)!.dec).toBe(g.acts[1]!.k);
  });

  it("视图切换：原文 / 效果 / 对比（E、C 快捷键）", () => {
    go("b5");
    click($$(".rvi").find((e) => e.textContent!.includes("第") && !e.textContent!.includes("已加密"))!);
    press("对比", ".rvc");
    expect($(".rvw.cmp")).toBeTruthy();
    key("c");
    expect($(".rvw.cmp")).toBeNull();
    key("e");
    expect(button("效果", $(".rvc")!).getAttribute("aria-pressed")).toBe("true");
  });

  it("悬停决策选项时效果预览跟着变化", () => {
    go("b5");
    click($$(".rvi").find((e) => e.textContent!.includes("第") && !e.textContent!.includes("已加密"))!);
    press("效果", ".rvc");
    expect($("#rvFxL")!.textContent).toContain("涂抹");
    act(() => { $$(".dopt")[1]!.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); $$(".dopt")[1]!.focus(); });
    expect($("#rvFxL")!.textContent).toContain("保留");
  });

  it("密码、整份失败与严格方式使用专用面板", () => {
    go("b5");
    expect($("#rvPwd")).toBeTruthy();
    // Enter without a password only asks for one
    key("Enter", { bubbles: true });
    const pw = $("#rvPwd") as HTMLInputElement;
    typeInto(pw, "x");
    act(() => { pw.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });
    expect(batch().rv!.items[0]!.dec).toBe("pwd");
    const fail = $$(".rvi").find((e) => e.textContent!.includes("整份文件"));
    if (fail) {
      click(fail);
      expect(win()).toContain("整份文件处理失败");
      expect(win()).toContain("重新处理会从失败的步骤再执行一次");
    }
  });

  it("全部决定后显示复核完成与后台导出进度", () => {
    go("b5");
    for (let i = 0; i < 400 && batch().rv!.items.some((x) => !x.dec); i++) {
      const rv = batch().rv!;
      const it = rv.items.find((x) => x.key === rv.cur);
      if (!it) break;
      if (it.gid === "pwd") {
        typeInto("#rvPwd", "pw");
        click($$(".dopt")[0]!);
      } else click($$(".dopt")[0]!);
    }
    expect(win()).toContain("复核完成");
    expect($("#rvExp")).toBeTruthy();
    expect($("#rvExp")!.textContent).toMatch(/正在导出你复核的|你复核的/);
  });
});

describe("批量 · 结果与清单", () => {
  it("完成页：统计、打开导出文件夹、查看清单", () => {
    go("b3");
    expect(win()).toContain("已导出 ");
    expect(win()).toContain("打开导出文件夹");
    expect(win()).toContain("按策略处理，未声明零遗漏");
    press("打开导出文件夹");
    expect($(".toast")!.textContent).toContain("已在访达中显示");
    press("查看清单");
    expect($("#tbTitle")!.textContent).toContain("导出清单");
  });

  it("导出清单：筛选与分页（80 行/页），可保存清单", () => {
    go("b4");
    expect($$("tbody tr").length).toBe(80);
    press("再显示 80");
    expect($$("tbody tr").length).toBe(160);
    press("未导出", ".ftools");
    expect($$("tbody tr").length).toBeLessThanOrEqual(10);
    expect(win()).toContain("无法处理");
    press("保存清单");
    expect($(".toast")!.textContent).toContain("已保存 manifest.json");
    press("全部", ".ftools");
  });

  it("无人值守：例外搁置，结果显示待你处理并可“去处理”", () => {
    go("b6");
    expect(win()).toContain("待你处理");
    press("去处理");
    expect($("#tbTitle")!.textContent).toContain("人工复核");
  });

  it("验证后自动导出关闭时出现“待导出”横幅与导出确认", () => {
    go("b1");
    press("验证后自动导出");
    press("开始处理");
    tick(240 * 40);
    const b = $$("button").find((e) => /^导出 \d+ 份$/.test(e.textContent!.trim()));
    expect(b).toBeTruthy();
    click(b!);
    expect($("#layer .modal")!.textContent).toContain("含人工决定");
    press("导出", "#layer .modal");
    expect($(".toast")!.textContent).toContain("已导出");
  });
});

describe("批量 · 打开其中单个文件", () => {
  it("文件标签页点击已检测文件进入复核，完成返回批次", () => {
    go("b2");
    press("文件", "[role=tablist]");
    const idx = batch().files.findIndex((f) => f.det && ["done", "ready"].includes(f.st));
    expect(idx).toBeGreaterThanOrEqual(0);
    click($$("tbody tr").find((r) => r.textContent!.includes(batch().files[idx]!.id))!);
    expect($("#tbTitle")!.textContent).toContain(batch().files[idx]!.name);
    expect(win()).toContain("已导出 · 修改后将重新导出并替代旧版本");
    expect($(".sum-row.ro")).toBeTruthy();
    press("完成");
    expect($("#tbTitle")!.textContent).toMatch(/批次|导出清单|人工复核/);
  });

  it("修改已导出的文件：完成并重新导出，旧版本被替代", () => {
    go("b2");
    const idx = batch().files.findIndex((f) => f.det && f.st === "done");
    press("文件", "[role=tablist]");
    click($$("tbody tr").find((r) => r.textContent!.includes(batch().files[idx]!.id))!);
    click("#h0");
    press("保留", "#layer .hp");
    press("完成并重新导出");
    expect(batch().files[idx]!.ver).toBe(2);
    expect(batch().files[idx]!.st).toBe("proc");
    expect($(".toast")!.textContent).toContain("该文件将重新导出并替代旧版本");
  });

  it("已完成批次修改文件后会重启导出并停止空闲时钟", () => {
    go("b3");
    expect(timers.batch).toBeUndefined();
    const idx = batch().files.findIndex((f) => f.st === "done");
    act(() => openBatchFile(idx));
    click("#h0");
    press("保留", "#layer .hp");
    press("完成并重新导出");
    expect(timers.batch).toBeDefined();
    tick(10000);
    expect(batch().files[idx]!.st).toBe("done");
    expect(batch().phase).toBe("result");
    expect(timers.batch).toBeUndefined();
  });

  it("尚未检测完成的文件打不开", () => {
    go("b2");
    press("文件", "[role=tablist]");
    press("进行中", ".ftools");
    const row = $$("tbody tr").find((r) => /排队|处理中/.test(r.textContent!));
    expect(row).toBeTruthy();
    click(row!);
    expect($(".toast")!.textContent).toContain("尚未检测完成");
    expect($("#tbTitle")!.textContent).toContain("批次");
  });
});
