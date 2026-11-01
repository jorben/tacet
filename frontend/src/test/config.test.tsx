import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "@testing-library/react";
import { getState, store } from "../store";
import { $, $$, button, click, go, mountApp, pick, press, tick, type as typeInto, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => {
  vi.useFakeTimers();
});

const preset = () => getState().presets.find((x) => x.id === getState().presetSel)!;
/** The control of the row titled `title` (a select button, switch, …). */
const rowControl = (title: string, sel = "button") => $$(".row").find((r) => r.querySelector(".rl")?.textContent?.startsWith(title))!.querySelector<HTMLElement>(sel)!;

describe("方案", () => {
  it("内建方案只读；列表含新建与导入", () => {
    go("presets");
    expect(win()).toContain("办公外发");
    expect(win()).toContain("内建");
    expect(win()).not.toContain("公司名");
    for (const label of ["账号", "IP 地址", "网址", "凭证"]) expect(win()).toContain(label);
    expect(button("仅规则").hasAttribute("disabled")).toBe(true);
    expect(button("新建")).toBeTruthy();
    expect(button("导入")).toBeTruthy();
  });

  it("复制后可编辑：检测方式、类别处理、最低置信度提示、版本递增", () => {
    go("presets");
    press("复制");
    expect(win()).toContain("办公外发 副本");
    press("仅规则");
    expect(win()).not.toContain("范围外");
    expect($$(".row").some((r) => r.querySelector(".rl")?.textContent === "人名")).toBe(false);
    expect(win()).not.toContain("IP 地址");
    press("模型 + 规则");
    expect(win()).toContain("IP 地址");
    click($$(".row").find((r) => r.querySelector(".rl")?.textContent === "电话")!.querySelectorAll("button")[1]!);
    expect(preset().cats.phone).toBe("ph");
    expect(preset().ver).toMatch(/^v\d+$/);
    click(rowControl("最低置信度"));
    pick("0.90");
    expect(win()).toContain("可能漏检");
  });

  it("切换模型同步更新类别，新增类别的处理方式可以编辑", () => {
    go("presets");
    press("复制");
    act(() => store.update((d) => {
      d.models.push({ ...d.models[0]!, id: "privacy", name: "Privacy Filter", categories: ["person", "address", "date", "phone", "email", "account", "url", "secret"] });
    }));
    click(rowControl("账号"));
    expect(preset().cats.account).toBe("mask");
    const accountButtons = $$(".row").find((r) => r.querySelector(".rl")?.textContent === "账号")!.querySelectorAll("button");
    click(accountButtons[2]!);
    expect(preset().cats.account).toBe("off");
    click(rowControl("模型"));
    pick("Privacy Filter");
    expect(win()).not.toContain("IP 地址");
    expect(win()).toContain("账号");
    expect(win()).toContain("凭证");
    click(rowControl("模型"));
    pick("AstrLink Guard");
    expect(win()).toContain("IP 地址");
    expect(preset().cats.account).toBe("off");
  });

  it("选择同一模型保持已设置的置信度", () => {
    go("presets");
    press("复制");
    click(rowControl("最低置信度"));
    pick("0.80");
    expect(preset().conf).toBe(0.8);
    click(rowControl("模型"));
    pick("AstrLink Guard");
    // same model: nothing to reset
    expect(preset().conf).toBe(0.8);
  });

  it("规则集多选至少保留一个；历史版本回滚；删除", () => {
    go("presets");
    press("简历脱敏", ".plist");
    click(rowControl("规则集"));
    pick("金融行业规则包");
    // the menu stays available for further picks
    expect($("#layer .pop")).toBeTruthy();
    expect(preset().rules).toContain("fin");
    pick("金融行业规则包");
    pick("企业词条");
    expect(preset().rules).toEqual(["cn"]);
    pick("中国场景规则集");
    expect($(".toast")!.textContent).toContain("至少保留一个规则集");
    expect(preset().rules).toEqual(["cn"]);
    press("更多");
    pick("历史版本");
    expect($("#layer .modal")!.textContent).toContain("保留最近 5 个版本");
    press("回滚");
    expect($(".toast")!.textContent).toContain("的内容保存为");
    press("复制");
    press("更多");
    pick("删除");
    expect($(".toast")!.textContent).toContain("已删除");
  });

  it("版本记录最多保留 5 个，回滚另存为新版本", () => {
    go("presets");
    press("复制");
    const phoneButtons = () => $$(".row").find((r) => r.querySelector(".rl")?.textContent === "电话")!.querySelectorAll("button");
    for (let i = 0; i < 8; i++) click(phoneButtons()[i % 2 ? 1 : 2]!);
    expect(preset().ver).toBe("v9");
    // current + the latest four earlier ones = five versions
    expect(preset().hist).toHaveLength(4);
    press("更多");
    pick("历史版本");
    expect($$("#layer .modal .row")).toHaveLength(5);
    press("回滚");
    expect(preset().ver).toBe("v10");
    expect(preset().hist).toHaveLength(4);
  });

  it("导入同名方案提示名称冲突并要求改名", () => {
    go("presets");
    press("导入");
    expect($("#layer .modal")!.textContent).toContain("名称冲突");
    typeInto("#impName", "简历脱敏");
    press("改名导入");
    expect($("#impErr")!.textContent).toContain("仍与已有名称重复");
    typeInto("#impName", "简历脱敏 (导入)");
    press("改名导入");
    expect($(".toast")!.textContent).toContain("已导入");
    expect(win()).toContain("简历脱敏 (导入)");
  });

  it("新建方案", () => {
    go("presets");
    press("新建");
    expect(win()).toContain("新方案");
    expect(win()).not.toContain("公司名");
  });
});

describe("规则", () => {
  it("内建规则集只读；企业词条可添加、编辑、删除规则，版本递增", () => {
    go("presets");
    press("规则", "[role=tablist]");
    expect(win()).toContain("手机号");
    expect($$(".row button.tg")[0]!.hasAttribute("disabled")).toBe(true);
    press("企业词条", ".plist");
    press("添加规则");
    expect($("#layer .modal")!.textContent).toContain("添加规则");
    typeInto("#rdName", "测试词");
    typeInto("#rdBody", "甲\n乙");
    press("保存", "#layer .modal");
    expect($(".toast")!.textContent).toContain("已保存为 v3");
    const rows = $$(".row").filter((r) => r.querySelector('button[aria-label="编辑"]'));
    click(rows.at(-1)!.querySelector('button[aria-label="编辑"]')!);
    press("删除", "#layer .modal");
    expect($(".toast")!.textContent).toContain("已删除");
  });

  it("编辑规则但内容未变时版本不变", () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("企业词条", ".plist");
    click($$('button[aria-label="编辑"]')[2]!);
    press("保存", "#layer .modal");
    expect($(".toast")!.textContent).toContain("内容未变，版本不变");
  });

  it("名称与内容必填", () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("企业词条", ".plist");
    press("添加规则");
    press("保存", "#layer .modal");
    expect(document.activeElement?.id).toBe("rdName");
    typeInto("#rdName", "x");
    press("保存", "#layer .modal");
    expect(document.activeElement?.id).toBe("rdBody");
  });

  it("正则不支持前后断言", () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("企业词条", ".plist");
    press("添加规则");
    press("正则", "#layer .modal");
    typeInto("#rdName", "x");
    typeInto("#rdBody", "(?<!a)b");
    press("保存", "#layer .modal");
    expect($(".toast")!.textContent).toContain("不支持前后断言");
    expect($("#layer .modal")).toBeTruthy();
  });

  it("新建规则集；被方案使用时不能删除", () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("新建");
    expect(win()).toContain("新规则集");
    press("企业词条", ".plist");
    press("更多");
    pick("删除");
    expect($(".toast")!.textContent).toContain("正在被");
  });

  it("插件规则包只在插件启用时可用", () => {
    go("plugins");
    press("启用 金融行业规则包");
    press("方案", "#rail");
    press("规则", "[role=tablist]");
    expect(win()).not.toContain("金融行业规则包");
    press("插件", "#rail");
    press("启用 金融行业规则包");
    press("方案", "#rail");
    expect(win()).toContain("金融行业规则包");
  });
});

describe("插件", () => {
  it("分组展示格式、规则与第三方，沙箱标签", () => {
    go("plugins");
    for (const t of ["模型", "格式", "规则", "第三方", "OFD", "金融行业规则包", "沙箱"]) expect(win()).toContain(t);
  });

  it("启用开关与停用提示", () => {
    go("plugins");
    press("启用 OFD");
    expect($(".toast")!.textContent).toContain("已停用");
    press("启用 OFD");
    expect($(".toast")!.textContent).toContain("已启用");
  });

  it("安装：Git/本地文件夹 → 审查权限 → 安装", () => {
    go("plugins");
    press("安装", ".phead");
    press("本地文件夹");
    click("#layer .modal button.field");
    expect(win()).toContain("~/plugins/tacet-eml");
    press("Git 仓库");
    press("下一步");
    expect($("#layer .modal")!.textContent).toContain("无网络");
    press("安装", "#layer .modal");
    expect(win()).toContain("EML 邮件");
    expect($("#layer .modal")).toBeNull();
  });

  it("更多菜单：授权完成判定；被任务引用时卸载需确认", () => {
    go("plugins");
    const more = (name: string) => click($$(".row").find((r) => r.textContent!.includes(name))!.querySelector('button[aria-label="更多"]')!);
    more("OFD");
    pick("授权完成判定");
    expect($(".toast")!.textContent).toContain("已按策略完成");
    more("OFD");
    pick("卸载");
    expect($("#layer .modal")!.textContent).toContain("1 个可恢复任务在使用此版本");
    press("作废任务并卸载");
    expect(win()).not.toContain("tacet-ofd");
  });

  it("没有被任务引用的插件直接卸载", () => {
    go("plugins");
    click($$(".row").find((r) => r.textContent!.includes("金融行业规则包") && r.querySelector('button[aria-label="更多"]'))!.querySelector('button[aria-label="更多"]')!);
    pick("卸载");
    expect($(".toast")!.textContent).toBe("已卸载");
    expect($("#layer .modal")).toBeNull();
  });
});

describe("设置", () => {
  it("外观切换写入 data-theme", () => {
    go("settings");
    press("深色");
    expect(document.documentElement.dataset.theme).toBe("dark");
    press("浅色");
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("关闭行为、资源档位、保留天数、涂抹样式", () => {
    go("settings");
    press("关闭程序");
    expect(win()).toContain("进行中的任务会暂停");
    press("标准");
    expect(win()).toContain("内存 4 GiB");
    click(rowControl("中断后可恢复"));
    pick("30");
    expect(win()).toContain("保留 30 天");
    expect($$(".rtile")).toHaveLength(12);
    press("粉色爱心");
    expect(document.documentElement.dataset.redact).toBe("heart");
    expect(button("粉色爱心").getAttribute("aria-checked")).toBe("true");
  });

  it("清理工作区、检查更新（含未完成任务的更新对话框）、关于", () => {
    go("settings");
    press("清理…");
    expect($("#layer .modal")!.textContent).toContain("清理工作区");
    press("清理", "#layer .modal");
    expect($(".toast")!.textContent).toContain("已清理");
    press("检查更新");
    expect(win()).toContain("检查中…");
    tick(1000);
    expect(win()).toContain("发现新版本 1.0.1");
    press("更新", ".version-controls");
    expect($("#layer .modal")!.textContent).toContain("更新功能尚未接入");
    press("更新", "#layer .modal");
    tick(1300);
    expect($("#currentVersion")!.textContent).toBe("1.0.1");
    press("Discord");
    expect($("#layer .modal")!.textContent).toContain("地址待配置");
    press("知道了");
    press("开源许可");
    expect($("#layer .modal")!.textContent).toContain("Apache");
  });

  it("有未完成任务时更新需选择：作废并更新会清掉它们", () => {
    go("b2");
    press("设置", "#rail");
    press("检查更新");
    tick(1000);
    press("更新", ".version-controls");
    expect($("#layer .modal")!.textContent).toContain("个任务未完成");
    press("作废并更新");
    expect(getState().batch).toBeNull();
    tick(1300);
    expect($(".toast")!.textContent).toContain("已作废未完成的任务");
  });

  it("自动检查更新开关", () => {
    go("settings");
    const before = $("#autoUpdate")!.getAttribute("aria-checked");
    press("自动检查更新");
    expect($("#autoUpdate")!.getAttribute("aria-checked")).not.toBe(before);
  });

  it("没有更新时提示已是最新版本", () => {
    go("settings");
    act(() => store.update((d) => { d.update.current = "1.0.1"; }));
    press("检查更新");
    tick(1000);
    expect(win()).toContain("已是最新版本");
    expect($("#updateApp")).toBeNull();
  });
});
