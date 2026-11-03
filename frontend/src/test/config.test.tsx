import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "@testing-library/react";
import { getState, store } from "../store";
import { $, $$, button, buttons, click, go, key, mountApp, pick, press, tick, type as typeInto, win } from "./helpers";

beforeAll(mountApp);
beforeEach(() => {
  vi.useFakeTimers();
});

const preset = () => getState().presets.find((x) => x.id === getState().presetSel)!;
/** The control of the row titled `title` (a select button, switch, …). */
const rowControl = (title: string, sel = "button") => $$(".row").find((r) => r.querySelector(".rl")?.textContent?.startsWith(title))!.querySelector<HTMLElement>(sel)!;
/** The scheme row of a category, its detection source, and whether its treatment can be changed. */
const catRow = (name: string) => $$(".row").find((r) => r.querySelector(".rl")?.textContent === name);
/** “不识别” for a category folded under the undetected heading, else its source tags (“模型 + 规则”). */
const sourceOf = (name: string) => {
  const r = catRow(name);
  if (!r) return undefined;
  return r.closest(".cat-fold") ? "不识别" : $$("[data-source]", r).map((t) => t.textContent).join(" + ");
};
const locked = (name: string) => [...catRow(name)!.querySelectorAll<HTMLButtonElement>(".seg button")].every((b) => b.disabled);
const pressed = (name: string) => catRow(name)!.querySelector(".seg button[aria-pressed=true]")?.textContent ?? catRow(name)!.querySelector(".cat-off")?.textContent;
const treatments = (name: string) => catRow(name)!.querySelectorAll<HTMLButtonElement>(".seg button");

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
    // every category is listed; what nothing detects any more is off and cannot be switched on
    expect(sourceOf("人名")).toBe("不识别");
    expect(locked("人名")).toBe(true);
    expect(pressed("人名")).toBe("关闭");
    expect(preset().cats.person).not.toBe("off"); // the setting stays for when a source comes back
    expect(sourceOf("IP 地址")).toBe("不识别");
    press("模型 + 规则");
    expect(sourceOf("IP 地址")).toBe("模型");
    expect(locked("人名")).toBe(false);
    click(treatments("电话")[1]!);
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
    click(rowControl("账号", ".seg button"));
    expect(preset().cats.account).toBe("mask");
    click(treatments("账号")[2]!);
    expect(preset().cats.account).toBe("off");
    click(rowControl("模型"));
    pick("Privacy Filter");
    expect(sourceOf("IP 地址")).toBe("不识别");
    expect(locked("IP 地址")).toBe(true);
    expect(sourceOf("账号")).toMatch(/^模型/);
    expect(sourceOf("凭证")).toMatch(/^模型/);
    click(rowControl("模型"));
    pick("AstrLink Guard");
    expect(sourceOf("IP 地址")).toBe("模型");
    expect(preset().cats.account).toBe("off");
  });

  it("每个类别标出检测来源；规则覆盖的类别换模型后仍在，来源随之变化", () => {
    go("presets");
    press("复制");
    act(() => store.update((d) => {
      d.models.push({ ...d.models[0]!, id: "privacy", name: "Privacy Filter", categories: ["person", "address", "date", "phone", "email", "account", "url", "secret"] });
    }));
    const source = sourceOf;
    expect(source("人名")).toBe("模型");
    expect(source("银行卡")).toBe("模型 + 规则");
    expect(source("身份证")).toBe("规则");
    click(rowControl("模型"));
    pick("Privacy Filter");
    expect(source("银行卡")).toBe("规则");
    expect(source("人名")).toBe("模型");
    press("仅规则");
    expect(source("电话")).toBe("规则");
  });

  it("无来源类别的提示只在方案自己存有遮盖或占位符时说设置会保留", () => {
    const kept = (name: string) => catRow(name)!.querySelector(".cat-kept")?.getAttribute("data-tip") ?? "";
    go("presets");
    press("复制");
    press("仅规则");
    // the copy holds the builtin's setting for 人名: it is stored and applies again with a source
    expect(kept("人名")).toContain("可识别后会再次生效");
    press("新建");
    press("仅规则");
    // a new scheme holds nothing for 人名: the default mask is not stored for a category nothing detects
    expect(preset().cats).not.toHaveProperty("person");
    expect(sourceOf("人名")).toBe("不识别");
    expect(kept("人名")).toBe("");
  });

  it("不识别的类别默认折叠在列表末尾，组头写数量，原因在页面里只出现一次", () => {
    go("presets");
    click($$(".pitem").find((b) => b.textContent!.includes("日志清理"))!);
    const fold = $(".cat-fold") as HTMLDetailsElement;
    expect(fold.open).toBe(false);
    const folded = $$(".cat-row", fold).map((r) => r.querySelector(".cat-name")!.textContent);
    expect(folded).toEqual(expect.arrayContaining(["人名", "地址", "账号", "IP 地址", "网址"]));
    expect($(".cat-fold-head")!.textContent).toContain("不识别的类别（" + folded.length + "）");
    // detected categories first, the fold last
    expect($(".cat-list")!.lastElementChild).toBe(fold);
    expect(document.body.innerHTML.split("只能关闭").length - 1).toBe(1);
    expect($(".preset-detail")!.textContent).toContain(folded.length ? "个可设置" : "");
    // another scheme starts folded again
    fold.open = true;
    press("简历脱敏", ".plist");
    press("日志清理", ".plist");
    expect(($(".cat-fold") as HTMLDetailsElement).open).toBe(false);
  });

  it("来源 Tag 与头部引用可点击：跳到对应页签并选中对象；模型页选中的模型切走再回来保持不变", () => {
    go("presets");
    act(() => store.update((d) => { d.models.push({ ...d.models[0]!, id: "privacy", name: "Privacy Filter", mapping: undefined }); }));
    press("简历脱敏", ".plist");
    // 身份证 is detected by the rules of 中国场景规则集
    click(catRow("身份证")!.querySelector("[data-source=rules]")!);
    expect(getState().presetTab).toBe("rules");
    expect(getState().rulesetSel).toBe("cn");
    expect($(".rule-group[data-category=id_number]")).toBeTruthy();
    // “用于 …” leads back to the scheme
    press("简历脱敏", ".preset-detail .phead-sub");
    expect(getState().presetTab).toBe("scheme");
    expect(preset().name).toBe("简历脱敏");
    click(catRow("人名")!.querySelector("[data-source=model]")!);
    expect(getState().presetTab).toBe("models");
    expect(getState().modelSel).toBe("astr");
    expect($(".preset-detail .h1")!.textContent).toBe("AstrLink Guard");
    // the model chosen on the models tab stays when coming back
    press("Privacy Filter", ".plist");
    press("方案", "[role=tablist]");
    press("模型", "[role=tablist]");
    expect($(".preset-detail .h1")!.textContent).toBe("Privacy Filter");
    // the scheme head links its model and rule sets
    press("方案", "[role=tablist]");
    press("AstrLink Guard", ".preset-detail .phead-sub");
    expect(getState().presetTab).toBe("models");
    expect($(".preset-detail .h1")!.textContent).toBe("AstrLink Guard");
    act(() => store.update((d) => { d.models = d.models.filter((m) => m.id !== "privacy"); }));
  });

  it("三个页签的头部都是两行；左栏新建与导入在栏顶不随列表滚动，只读条目带锁", () => {
    go("presets");
    for (const tab of ["方案", "规则", "模型"]) {
      press(tab, "[role=tablist]");
      expect($$(".preset-detail .phead .phead-title")).toHaveLength(1);
      expect($$(".preset-detail .phead .phead-sub")).toHaveLength(1);
      expect($(".plist")!.contains(buttons("新建")[0] ?? null)).toBe(false);
    }
    press("方案", "[role=tablist]");
    const head = $(".plist-head")!;
    expect(button("新建", head)).toBeTruthy();
    expect(button("导入", head)).toBeTruthy();
    const lock = (name: string) => !!$$(".pitem").find((b) => b.textContent!.includes(name))!.querySelector(".pn-lock");
    expect(lock("办公外发")).toBe(true);
    expect(lock("简历脱敏")).toBe(false);
    press("规则", "[role=tablist]");
    expect(lock("中国场景规则集")).toBe(true);
    expect(lock("企业词条")).toBe(false);
  });

  it("内建方案在头部下方说明只读，可直接复制；控件置灰；图像与嵌入是说明行", () => {
    go("presets");
    expect($(".config-note")!.textContent).toContain("内建方案，只读。");
    expect(button("仅规则")).toBeDisabled();
    expect(win()).toContain("固定涂抹，暂不支持修改");
    press("复制后编辑", ".config-note");
    expect(preset().name).toBe("办公外发 副本");
    expect($(".config-note")).toBeNull();
  });

  it("方案选用的规则组都不可用时，规则组一行说明原因而不是只写未选择", () => {
    go("presets");
    press("复制");
    act(() => store.update((d) => { d.presets.find((x) => x.id === d.presetSel)!.rules = ["gone"]; }));
    expect(rowControl("规则组").textContent).toContain("所选规则组不可用");
    expect(rowControl("规则组").querySelector("[data-tip]")!.getAttribute("data-tip")).toContain("已删除或所属插件已停用");
  });

  it("没有方案、没有规则组时保留左栏的新建与导入，右侧给出引导", () => {
    go("presets");
    act(() => store.update((d) => { d.presets = []; }));
    expect($(".guide-card")!.textContent).toContain("还没有方案");
    expect(button("新建", $(".plist-head")!)).toBeTruthy();
    press("新建方案", ".guide-card");
    expect(preset().name).toBe("新方案");
    press("规则", "[role=tablist]");
    act(() => store.update((d) => { d.rulesets = []; }));
    expect($(".guide-card")!.textContent).toContain("还没有规则组");
    expect(button("导入", $(".plist-head")!)).toBeTruthy();
    press("新建规则组", ".guide-card");
    expect(win()).toContain("新规则组");
    press("模型", "[role=tablist]");
    const models = getState().models;
    act(() => store.update((d) => { d.models = []; }));
    expect($(".guide-card")!.textContent).toContain("还没有模型");
    press("去插件页安装", ".guide-card");
    expect(getState().view).toBe("plugins");
    act(() => store.update((d) => { d.models = models; }));
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
    click(rowControl("规则组"));
    pick("金融行业规则包");
    // the menu stays available for further picks
    expect($("#layer .pop")).toBeTruthy();
    expect(preset().rules).toContain("fin");
    pick("金融行业规则包");
    pick("企业词条");
    expect(preset().rules).toEqual(["cn"]);
    pick("中国场景规则集");
    expect($(".toast")!.textContent).toContain("至少保留一个规则组");
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
    const phoneButtons = () => treatments("电话");
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

  it("测试：入口在复制左侧；识别文本并标出每条结果来自模型还是规则；改文本清除旧结果", async () => {
    go("presets");
    const head = $(".preset-detail .phead")!;
    const labels = $$("button", head).map((b) => b.textContent);
    expect(labels.indexOf("测试")).toBe(labels.indexOf("复制") - 1);
    press("测试", ".preset-detail .phead");
    const modal = () => $(".pt-modal")!;
    expect(modal().textContent).toContain("方案「办公外发」· AstrLink Guard + 规则");
    expect(button("识别", modal()).hasAttribute("disabled")).toBe(true);
    typeInto("#ptText", "联系人：王晓明，手机 13812345678，证件 11010519491231002X");
    press("识别", ".pt-modal");
    await act(async () => {});
    expect($(".pt-sum")!.textContent).toBe("识别到 3 处 · 模型 1 · 规则 1 · 模型 + 规则 1");
    const row = (v: string) => $$(".pt-row").find((r) => r.querySelector(".pt-val")?.textContent === v)!;
    expect($$("[data-source]", row("王晓明")).map((t) => t.textContent)).toEqual(["模型 0.88"]);
    expect($$("[data-source]", row("13812345678")).map((t) => t.textContent)).toEqual(["模型 0.97", "规则 · 手机号"]);
    expect($$("[data-source]", row("11010519491231002X")).map((t) => t.textContent)).toEqual(["规则 · 居民身份证"]);
    expect($$(".pt-text mark").map((m) => m.textContent)).toEqual(["王晓明", "13812345678", "11010519491231002X"]);
    expect($$(".pt-text mark")[1]!.getAttribute("data-tip")).toBe("电话 · 模型 + 规则 0.97 · 手机号");
    typeInto("#ptText", "联系人：王晓明");
    expect($(".pt-sum")).toBeNull();
    press("关闭", ".pt-modal");
    expect($(".pt-modal")).toBeNull();
  });

  it("测试：模型未就绪时说明原因；仅规则方案只列规则命中", async () => {
    go("presets");
    act(() => store.update((d) => { d.models[0]!.status = "missing"; }));
    press("测试", ".preset-detail .phead");
    typeInto("#ptText", "手机 13812345678");
    press("识别", ".pt-modal");
    await act(async () => {});
    expect($(".pt-err")!.textContent).toContain("模型尚未就绪");
    press("关闭", ".pt-modal");
    click($$(".pitem").find((b) => b.textContent!.includes("日志清理"))!);
    press("测试", ".preset-detail .phead");
    expect($(".pt-modal")!.textContent).toContain("仅规则");
    typeInto("#ptText", "联系人：王晓明，手机 13812345678");
    press("识别", ".pt-modal");
    await act(async () => {});
    expect($(".pt-sum")!.textContent).toBe("识别到 1 处 · 规则 1");
    expect($$("[data-source=model]")).toHaveLength(0);
    press("关闭", ".pt-modal");
  });
});

describe("规则", () => {
  it("内建规则集只读；企业词条可添加、编辑、删除规则，版本递增", () => {
    go("presets");
    press("规则", "[role=tablist]");
    expect(win()).toContain("手机号");
    expect($('button[aria-label="启用 手机号"]')!.hasAttribute("disabled")).toBe(true);
    expect($('[role=checkbox][aria-label="启用类别 电话"]')!.hasAttribute("disabled")).toBe(true);
    press("企业词条", ".plist");
    press("添加规则");
    expect($("#layer .modal")!.textContent).toContain("添加规则");
    typeInto("#rdName", "测试词");
    typeInto("#rdBody", "甲\n乙");
    press("保存", "#layer .modal");
    expect($(".toast")!.textContent).toContain("已保存为 v3");
    click(button("编辑", $$(".rule-row").find((r) => r.querySelector(".rule-name")?.textContent === "测试词")!));
    press("删除", "#layer .modal");
    expect($(".toast")!.textContent).toContain("已删除");
  });

  it("编辑规则但内容未变时版本不变", () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("企业词条", ".plist");
    click(button("编辑", $$(".rule-row").find((r) => r.querySelector(".rule-name")?.textContent === "员工工号")!));
    press("保存", "#layer .modal");
    expect($(".toast")!.textContent).toContain("内容未变，版本不变");
  });

  it("规则的命中归类来自类别目录：任何目录中的类别都可选，规则页与方案页同名", () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("企业词条", ".plist");
    const row = $$(".row").find((r) => r.querySelector(".rule-name")?.textContent === "员工工号")!;
    expect(row.closest(".rule-group")!.querySelector(".cat-name")!.textContent).toBe("账号");
    press("添加规则");
    const options = $$("#layer .typegrid button:not(.typegrid-add):not(.typegrid-del)").map((b) => b.textContent);
    expect(options).toEqual(getState().categories.map((c) => c.name));
    expect(options).toEqual(expect.arrayContaining(["账号", "IP 地址", "网址", "凭证", "公司名", "其他"]));
    press("取消", "#layer .modal");
  });

  it("写规则时新建类别：选中新类别保存后，规则页与方案页都按它的名称列出；同名被拒绝", async () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("企业词条", ".plist");
    press("添加规则");
    typeInto("#rdName", "代号");
    typeInto("#rdBody", "北斗\n银河");
    press("新建类别", "#layer .modal");
    typeInto("#rdCatName", "电话");
    await act(async () => { press("创建", "#layer .newcat"); });
    expect($("#layer .newcat")!.textContent).toContain("已有同名类别");
    typeInto("#rdCatName", "项目代号");
    await act(async () => { press("创建", "#layer .newcat"); });
    const made = getState().categories.at(-1)!;
    expect(made).toMatchObject({ name: "项目代号", placeholder: "[项目代号]", origin: "custom", manual: true });
    expect($("#layer .newcat")).toBeNull();
    expect($$("#layer .typegrid button[aria-pressed=true]").map((b) => b.textContent)).toEqual(["项目代号"]);
    press("保存", "#layer .modal");
    const row = $$(".row").find((r) => r.querySelector(".rule-name")?.textContent === "代号")!;
    expect(row.closest(".rule-group")!.querySelector(".cat-name")!.textContent).toBe("项目代号");
    expect(getState().rulesets.find((r) => r.name === "企业词条")!.rules.at(-1)!.cat).toBe(made.id);
    // 简历脱敏 uses the enterprise terms: the new category is one of its categories, with the rules as source
    press("方案", "[role=tablist]");
    press("简历脱敏", ".plist");
    const cat = $$(".row").find((r) => r.querySelector(".rl")?.textContent === "项目代号");
    expect(cat?.querySelector("[data-source]")!.textContent).toBe("规则");
  });

  it("模型页签：逐个标签改计入的类别、不使用、恢复默认；方案页的类别随之变化", async () => {
    go("presets");
    press("模型", "[role=tablist]");
    expect(win()).toContain("AstrLink Guard");
    expect(win()).toContain("默认映射");
    const row = (category: string) => $(`.mapping-row[data-category="${category}"]`)!;
    expect(row("person").querySelector(".mapping-labels")!.textContent).toBe("private_person");
    // 公司名 is review-only: listed only once a label counts as it, as on the scheme page
    expect($('.mapping-row[data-category="organization"]')).toBeNull();
    // Move a label into another entity from the label's own menu.
    press("标签 private_person", '.mapping-row[data-category="person"]');
    await act(async () => { pick("公司名"); });
    expect($(".loss-modal")!.textContent).toContain("办公外发");
    await act(async () => { press("仍要保存映射", ".loss-modal"); });
    const astr = () => getState().models.find((m) => m.id === "astr")!;
    expect(astr().mapping?.map.private_person).toBe("organization");
    expect(row("person").querySelector('[data-tip="默认值：private_person"]')).toBeTruthy();
    expect(row("organization").textContent).toContain("private_person");
    expect($(".toast")!.textContent).toBe("已将 private_person 从 人名 移到 公司名");
    expect(win()).toContain("已自定义");
    // Uncheck a label and verify the unassociated section offers it for reassignment.
    press("地址 关联的标签", '.mapping-row[data-category="address"]');
    pick("private_address");
    await act(async () => { press("完成", "#layer .pop"); });
    await act(async () => { press("仍要保存映射", ".loss-modal"); });
    expect(astr().mapping?.map).not.toHaveProperty("private_address");
    expect($(".unmapped-labels")!.textContent).toContain("private_address");
    expect(astr().categories).toContain("organization");
    expect(astr().categories).not.toContain("person");
    expect(astr().categories).not.toContain("address");
    // the scheme lists every category, with what the model produces now as its source
    press("方案", "[role=tablist]");
    press("办公外发", ".plist");
    expect(sourceOf("公司名")).toBe("模型");
    expect(sourceOf("人名")).toBe("不识别");
    expect(sourceOf("地址")).toBe("不识别");
    // back to the default
    press("模型", "[role=tablist]");
    await act(async () => { press("恢复默认"); });
    if ($(".loss-modal")) await act(async () => { press("仍要保存映射", ".loss-modal"); });
    expect(astr().mapping).toBeUndefined();
    expect(astr().categories).toContain("person");
    expect(win()).toContain("默认映射");
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

  it("新建规则集；被方案使用时删除置灰并写明原因，点了也不删", () => {
    go("presets");
    press("规则", "[role=tablist]");
    press("新建");
    expect(win()).toContain("新规则组");
    press("企业词条", ".plist");
    press("更多");
    const del = $$("#layer .mi").find((e) => e.textContent!.includes("删除"))!;
    expect(del).toBeDisabled();
    expect(del.textContent).toContain("被「简历脱敏」使用");
    click(del);
    expect($(".toast")).toBeNull();
    expect(getState().rulesets.some((r) => r.name === "企业词条")).toBe(true);
    key("Escape");
    // one no scheme uses is deleted
    press("新规则组", ".plist");
    press("更多");
    expect($$("#layer .mi").find((e) => e.textContent!.includes("删除"))).toBeEnabled();
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
