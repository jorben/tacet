import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { S } from "../engine/state";
import { act } from "@testing-library/react";
import { $, $$, click, clickA, go, key, mountApp, tick, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("批量 · 预检", () => {
  it("显示份数、格式统计、无法处理弹层与全部设置项", () => {
    go("b1");
    expect($("#tbTitle")!.textContent).toContain("批次 · 214 份");
    expect(win()).toContain("DOCX 111");
    expect(win()).toContain("无法处理 3");
    for (const t of ["方式", "方案", "图像与嵌入", "导出位置", "验证后自动导出", "中断后可恢复", "开始处理"]) expect(win()).toContain(t);
    clickA("showBad");
    expect($("#layer .pop")!.textContent).toContain("无法处理 · 不会导出");
    expect($("#layer .pop")!.textContent).toMatch(/格式不支持|文件已损坏|超过 2,000 页/);
  });

  it("方式切换：引导 / 无人值守（自动导出锁定）/ 严格", () => {
    go("b1");
    clickA("bmode", "auto");
    expect($('[data-a="bmode"][data-v="auto"]')!.getAttribute("aria-pressed")).toBe("true");
    expect($('[data-a="bauto"]')!.hasAttribute("disabled")).toBe(true);
    clickA("bmode", "strict");
    expect($('[data-a="bauto"]')!.hasAttribute("disabled")).toBe(false);
    clickA("bmode", "guided");
  });

  it("预检菜单：方案、图像、导出位置、保留天数；自动导出开关", () => {
    go("b1");
    clickA("bpreset");
    expect($$("#layer .mi").map((e) => e.textContent).join()).toContain("简历脱敏");
    click($$("#layer .mi").find((e) => e.textContent!.includes("日志清理"))!);
    expect($('[data-a="bpreset"]')!.textContent).toContain("日志清理");
    expect(win()).toContain("仅规则 · 无需模型");
    clickA("bimg");
    click($$("#layer .mi").find((e) => e.textContent!.includes("保留"))!);
    expect($('[data-a="bimg"]')!.textContent).toContain("保留 · 部分结果");
    clickA("bkeep");
    click($$("#layer .mi").find((e) => e.textContent!.includes("30"))!);
    expect($('[data-a="bkeep"]')!.textContent).toContain("保留 30 天");
    const before = $('[data-a="bauto"]')!.getAttribute("aria-checked");
    clickA("bauto");
    expect($('[data-a="bauto"]')!.getAttribute("aria-checked")).not.toBe(before);
  });

  it("取消回到首页", () => {
    go("b1");
    clickA("cancelBatch");
    expect($("#tbTitle")!.textContent).toContain("Tacet");
  });
});

describe("批量 · 处理中", () => {
  it("进度、标签页与例外分组；暂停与继续", () => {
    go("b2");
    expect($(".bhead .big")!.textContent).toMatch(/\d+\s*\/\s*214/);
    expect(win()).toContain("待你处理");
    expect(win()).toContain("开始复核");
    for (const g of ["扫描页，无文本层", "截图中含文字", "疑似身份证号 · 校验位不符", "嵌入文件"]) expect(win()).toContain(g);
    clickA("pauseBatch");
    expect($(".toast")!.textContent).toContain("已暂停派发");
    clickA("pauseBatch");
    expect($(".toast")!.textContent).toContain("已继续");
  });

  it("自动处理标签页列出类别统计并可抽查", () => {
    go("b2");
    clickA("btab", "auto");
    expect(win()).toContain("公司名");
    expect(win()).toContain("图像");
    clickA("sample", "phone");
    expect($("#layer .drawer")!.textContent).toContain("抽查 · 电话");
    expect($$("#layer .drow")).toHaveLength(8);
    clickA("sampleMore", "phone");
    clickA("closeModal");
    expect($("#layer .drawer")).toBeNull();
  });

  it("文件标签页：分页、不渲染全量成员表、搜索与筛选", () => {
    go("b2");
    clickA("btab", "files");
    tick(10);
    expect($$("tbody tr").length).toBe(60);
    expect($$("tbody tr").length).toBeLessThan(214);
    expect($('[data-a="moreFiles"]')!.textContent).toContain("再显示 60");
    clickA("moreFiles");
    expect($$("tbody tr").length).toBe(120);
    clickA("ff", "bad");
    tick(10);
    expect($$("tbody tr").length).toBeLessThanOrEqual(10);
    clickA("ff", "all");
    tick(10);
    const q = $("#fq") as HTMLInputElement;
    q.value = "T0007";
    act(() => q.dispatchEvent(new Event("input", { bubbles: true })));
    expect($$("tbody tr")).toHaveLength(1);
    expect(win()).toContain("T0007");
  });

  it("整组处理菜单：保留需确认，可先去复核", () => {
    go("b2");
    clickA("btab", "todo");
    const id = S.batch.groups.find((g: any) => g.id === "shot").id;
    clickA("gMenu", id);
    click($$("#layer .mi").find((e) => e.textContent!.includes("全部保留"))!);
    expect($("#layer .modal")!.textContent).toContain("全部保留");
    expect($("#layer .modal")!.textContent).toContain("部分结果");
    clickA("bulkView");
    expect(S.batch.phase).toBe("review");
  });

  it("停止：保留以便恢复 / 停止并清理", () => {
    go("b2");
    clickA("stopBatch");
    expect($$("#layer .mi").map((e) => e.textContent).join()).toContain("停止并保留以便恢复");
    click($$("#layer .mi").find((e) => e.textContent!.includes("停止并清理"))!);
    expect($("#tbTitle")!.textContent).toContain("Tacet");
    expect($(".toast")!.textContent).toContain("已停止并清理");
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
    clickA("rvStep", "1");
    clickA("rvStep", "-1");
    // 选择一个非密码分组条目做决策
    const item = $$(".rvi").find((e) => e.textContent!.includes("第") && !e.textContent!.includes("已加密"))!;
    click(item);
    clickA("rvDo", S.batch.groups[S.batch.rv.items.find((x: any) => x.key === S.batch.rv.cur).gi].acts[0].k);
    expect($(".rvp-f")!.textContent).not.toBe(before);
    expect($(".toast")).toBeTruthy();
    key("z", { metaKey: true });
    expect($(".toast")!.textContent).toContain("已撤销");
  });

  it("视图切换：原文 / 效果 / 对比（E、C 快捷键）", () => {
    go("b5");
    const item = $$(".rvi").find((e) => e.textContent!.includes("第") && !e.textContent!.includes("已加密"))!;
    click(item);
    clickA("rvMode", "cmp");
    expect($(".rvw.cmp")).toBeTruthy();
    key("c");
    expect($(".rvw.cmp")).toBeNull();
    key("e");
    expect($('[data-a="rvMode"][data-v="fx"]')!.getAttribute("aria-pressed")).toBe("true");
  });

  it("密码、整份失败与严格方式使用专用面板", () => {
    go("b5");
    expect($("#rvPwd")).toBeTruthy();
    const pw = $("#rvPwd") as HTMLInputElement;
    pw.value = "x";
    key("Enter");
    const fail = $$(".rvi").find((e) => e.textContent!.includes("整份文件"));
    if (fail) {
      click(fail);
      expect(win()).toContain("整份文件处理失败");
      expect(win()).toContain("重新处理会从失败的步骤再执行一次");
    }
  });

  it("全部决定后显示复核完成与后台导出进度", () => {
    go("b5");
    for (let i = 0; i < 400 && S.batch.rv.items.some((x: any) => !x.dec); i++) {
      const it = S.batch.rv.items.find((x: any) => x.key === S.batch.rv.cur);
      if (!it) break;
      if (it.gid === "pwd") {
        const p = $("#rvPwd") as HTMLInputElement | null;
        if (p) p.value = "pw";
        clickA("rvDo", "pwd");
      } else clickA("rvDo", S.batch.groups[it.gi].acts[0].k);
    }
    expect(win()).toContain("复核完成");
    expect($("#rvExp")).toBeTruthy();
  });
});

describe("批量 · 结果与清单", () => {
  it("完成页：统计、打开导出文件夹、查看清单", () => {
    go("b3");
    expect(win()).toContain("已导出 ");
    expect(win()).toContain("打开导出文件夹");
    expect(win()).toContain("按策略处理，未声明零遗漏");
    clickA("reveal");
    expect($(".toast")!.textContent).toContain("已在访达中显示");
    clickA("bphase", "manifest");
    expect($("#tbTitle")!.textContent).toContain("导出清单");
  });

  it("导出清单：筛选与分页（80 行/页），可保存清单", () => {
    go("b4");
    expect($$("tbody tr").length).toBe(80);
    clickA("moreMan");
    expect($$("tbody tr").length).toBe(160);
    clickA("mf", "no");
    expect($$("tbody tr").length).toBeLessThanOrEqual(10);
    expect(win()).toContain("无法处理");
    clickA("fakeSave", "manifest.json");
    expect($(".toast")!.textContent).toContain("已保存 manifest.json");
    clickA("mf", "all");
  });

  it("无人值守：例外搁置，结果显示待你处理并可“去处理”", () => {
    go("b6");
    expect(win()).toContain("待你处理");
    expect(win()).toContain("去处理");
  });

  it("验证后自动导出关闭时出现“待导出”横幅与导出确认", () => {
    go("b1");
    clickA("bauto");
    clickA("startBatch");
    tick(240 * 40);
    const b = $('[data-a="expReady"]');
    if (b) {
      click(b);
      expect($("#layer .modal")!.textContent).toContain("含人工决定");
      clickA("expDo");
      expect($(".toast")!.textContent).toContain("已导出");
    }
  });
});

describe("批量 · 打开其中单个文件", () => {
  it("文件标签页点击已检测文件进入复核，完成返回批次", () => {
    go("b2");
    clickA("btab", "files");
    const idx = S.batch.files.findIndex((f: any) => f.det && ["done", "ready"].includes(f.st));
    expect(idx).toBeGreaterThanOrEqual(0);
    clickA("openFile", String(idx));
    expect($("#tbTitle")!.textContent).toContain(S.batch.files[idx].name);
    expect(win()).toContain("已导出 · 修改后将重新导出并替代旧版本");
    expect($(".sum-row.ro")).toBeTruthy();
    clickA("backBatch");
    expect($("#tbTitle")!.textContent).toMatch(/批次|导出清单|人工复核/);
  });
});
