import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { S } from "../engine/state";
import { $, $$, click, clickA, go, mountApp, tick, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => vi.useRealTimers());
const menuItem = (t: string) => $$("#layer .mi").find((e) => e.textContent!.includes(t))!;

describe("方案", () => {
  it("内建方案只读；列表含新建与导入", () => {
    go("presets");
    expect(win()).toContain("通用办公脱敏");
    expect(win()).toContain("内建");
    expect($('[data-a="pdet"]')!.hasAttribute("disabled")).toBe(true);
    expect($('[data-a="pnew"]')).toBeTruthy();
    expect($('[data-a="pimp"]')).toBeTruthy();
  });

  it("复制后可编辑：检测方式、类别处理、最低置信度提示、版本递增", () => {
    go("presets");
    clickA("pcopy");
    expect(win()).toContain("通用办公脱敏 副本");
    clickA("pdet", "rules");
    expect(win()).toContain("范围外");
    clickA("pdet", "model");
    clickA("pcat", "phone|ph");
    const p = S.presets.find((x: any) => x.id === S.presetSel);
    expect(p.cats.phone).toBe("ph");
    expect(p.ver).toMatch(/^v\d+$/);
    clickA("pconf");
    click(menuItem("0.90"));
    expect(win()).toContain("可能漏检");
  });

  it("规则集多选至少保留一个；历史版本回滚；删除", () => {
    go("presets");
    clickA("psel", "cv");
    clickA("prules");
    click(menuItem("金融行业规则包"));
    clickA("pmore");
    click(menuItem("历史版本"));
    expect($("#layer .modal")!.textContent).toContain("保留最近 5 个版本");
    clickA("verBack", "0");
    expect($(".toast")!.textContent).toContain("的内容保存为");
    clickA("pcopy");
    clickA("pmore");
    click(menuItem("删除"));
    expect($(".toast")!.textContent).toContain("已删除");
  });

  it("导入同名方案提示名称冲突并要求改名", () => {
    go("presets");
    clickA("pimp");
    expect($("#layer .modal")!.textContent).toContain("名称冲突");
    ($("#impName") as HTMLInputElement).value = "简历脱敏";
    clickA("impGo");
    expect($("#impErr")!.textContent).toContain("仍与已有名称重复");
    ($("#impName") as HTMLInputElement).value = "简历脱敏 (导入)";
    clickA("impGo");
    expect($(".toast")!.textContent).toContain("已导入");
  });
});

describe("规则", () => {
  it("内建规则集只读；企业词条可添加、编辑、删除规则，版本递增", () => {
    go("presets");
    clickA("ptab", "rules");
    expect(win()).toContain("手机号");
    expect($('[data-a="rtog"]')!.hasAttribute("disabled")).toBe(true);
    clickA("rsel", "corp");
    clickA("radd");
    expect($("#layer .modal")!.textContent).toContain("添加规则");
    ($("#rdName") as HTMLInputElement).value = "测试词";
    ($("#rdBody") as HTMLTextAreaElement).value = "甲\n乙";
    clickA("rdSave");
    expect($(".toast")!.textContent).toContain("已保存为 v3");
    clickA("redit", "3");
    clickA("rdDel");
    expect($(".toast")!.textContent).toContain("已删除");
  });

  it("正则不支持前后断言", () => {
    go("presets");
    clickA("ptab", "rules");
    clickA("rsel", "corp");
    clickA("radd");
    clickA("rdKind", "正则");
    ($("#rdName") as HTMLInputElement).value = "x";
    ($("#rdBody") as HTMLTextAreaElement).value = "(?<!a)b";
    clickA("rdSave");
    expect($(".toast")!.textContent).toContain("不支持前后断言");
  });

  it("新建规则集；被方案使用时不能删除", () => {
    go("presets");
    clickA("ptab", "rules");
    clickA("rsnew");
    expect(win()).toContain("新规则集");
    clickA("rsel", "corp");
    clickA("rsmore");
    click(menuItem("删除"));
    expect($(".toast")!.textContent).toContain("正在被");
  });
});

describe("插件", () => {
  it("分组展示格式、规则与第三方，沙箱标签", () => {
    go("plugins");
    for (const t of ["模型", "格式", "规则", "第三方", "OFD", "金融行业规则包", "沙箱"]) expect(win()).toContain(t);
  });

  it("启用开关与停用提示", () => {
    go("plugins");
    clickA("pltog", "ofd");
    expect($(".toast")!.textContent).toContain("已停用");
    clickA("pltog", "ofd");
    expect($(".toast")!.textContent).toContain("已启用");
  });

  it("安装：Git/本地文件夹 → 审查权限 → 安装", () => {
    go("plugins");
    clickA("plinstall");
    clickA("instKind", "dir");
    clickA("fakeDir");
    expect(win()).toContain("~/plugins/tacet-eml");
    clickA("instKind", "git");
    clickA("instNext");
    expect($("#layer .modal")!.textContent).toContain("无网络");
    clickA("instDo");
    expect(win()).toContain("EML 邮件");
  });

  it("更多菜单：授权完成判定；被任务引用时卸载需确认", () => {
    go("plugins");
    clickA("plmore", "ofd");
    click(menuItem("授权完成判定"));
    expect($(".toast")!.textContent).toContain("已按策略完成");
    clickA("plmore", "ofd");
    click(menuItem("卸载"));
    expect($("#layer .modal")!.textContent).toContain("1 个可恢复任务在使用此版本");
    clickA("plrm", "ofd");
    expect(win()).not.toContain("tacet-ofd");
  });
});

describe("设置", () => {
  it("外观切换写入 data-theme", () => {
    go("settings");
    clickA("theme", "dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    clickA("theme", "light");
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("关闭行为、资源档位、保留天数、涂抹样式", () => {
    go("settings");
    clickA("closeAct", "quit");
    expect(win()).toContain("进行中的任务会暂停");
    clickA("tier", "std");
    expect(win()).toContain("内存 4 GiB");
    clickA("skeep");
    click(menuItem("30"));
    expect(win()).toContain("保留 30 天");
    expect($$(".rtile")).toHaveLength(12);
    clickA("redact", "heart");
    expect(document.documentElement.dataset.redact).toBe("heart");
  });

  it("清理工作区、检查更新（含未完成任务的更新对话框）、关于", () => {
    go("settings");
    clickA("cleanWs");
    expect($("#layer .modal")!.textContent).toContain("清理工作区");
    clickA("cleanDo");
    expect($(".toast")!.textContent).toContain("已清理");
    clickA("checkUpd");
    expect(win()).toContain("检查中…");
    tick(1000);
    expect(win()).toContain("发现新版本 1.0.1");
    clickA("updateApp");
    expect($("#layer .modal")!.textContent).toContain("更新功能尚未接入");
    clickA("updateDo");
    tick(1300);
    expect($("#currentVersion")!.textContent).toBe("1.0.1");
    clickA("aboutLink", "discord");
    expect($("#layer .modal")!.textContent).toContain("地址待配置");
    clickA("closeModal");
    clickA("licenses");
    expect($("#layer .modal")!.textContent).toContain("Apache");
  });

  it("自动检查更新开关", () => {
    go("settings");
    const before = $("#autoUpdate")!.getAttribute("aria-checked");
    clickA("autoUpd");
    expect($("#autoUpdate")!.getAttribute("aria-checked")).not.toBe(before);
  });
});
