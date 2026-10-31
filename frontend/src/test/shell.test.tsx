import { beforeAll, describe, expect, it } from "vitest";
import { $, $$, go, key, mountApp, press, win } from "./helpers";
import { getService } from "../services";

beforeAll(mountApp);

describe("应用外壳与导航", () => {
  it("渲染标题栏、左侧导航与首页", () => {
    expect($("#win .titlebar")).toBeTruthy();
    const nav = $$("#rail button").map((b) => b.getAttribute("aria-label"));
    expect(nav).toEqual(["任务", "方案", "插件", "设置"]);
    expect($("#tbTitle")!.textContent).toContain("Tacet");
    expect(win()).toContain("该静默处，自有分寸");
  });

  it("左侧导航可到达方案、插件、设置并高亮当前项", () => {
    for (const [name, title] of [["方案", "方案"], ["插件", "插件"], ["设置", "设置"], ["任务", "Tacet"]] as const) {
      press(name, "#rail");
      expect($("#tbTitle")!.textContent).toContain(title);
      expect($(`#rail [aria-label="${name}"]`)!.getAttribute("aria-current")).toBe("page");
    }
  });

  it("子页面显示“返回”并回到来源页", () => {
    // the model guide only shows while no model is ready
    go("fresh");
    press("前往下载");
    expect($("#tbTitle")!.textContent).toContain("插件");
    press("返回");
    expect($("#tbTitle")!.textContent).toContain("Tacet");
  });

  it("浏览器/测试环境走 mock 服务，宿主为 browser", async () => {
    const s = getService();
    expect(s.host).toBe("browser");
    expect((await s.appInfo()).name).toBe("Tacet");
  });

  it("原型场景导航列出全部场景", () => {
    const ids = $$("#proto [data-scene]").map((b) => b.dataset.scene);
    for (const k of ["home", "fresh", "model-download", "model-paused", "model-offline", "model-disk", "model-integrity", "model-loading", "model-load", "model-wait", "model-batch", "model-plugins", "s1", "s2", "s5", "s4", "s3", "b1", "b2", "b5", "b3", "b6", "b4", "presets", "plugins", "settings", "close"]) {
      expect(ids).toContain(k);
    }
  });

  it("关闭窗口：托盘模式给出提示，退出模式在有任务时确认", () => {
    go("home");
    press("关闭窗口");
    expect($("#layer .toast")!.textContent).toContain("已最小化到托盘");
    go("settings");
    press("关闭程序");
    go("b1");
    press("关闭窗口");
    expect($("#layer .modal")!.textContent).toContain("有未完成的任务");
    expect($("#layer .modal")!.textContent).toContain("保存并关闭");
    expect($("#layer .modal")!.textContent).toContain("停止并清理");
  });

  it("Esc 先关闭弹出层，再关闭对话框", () => {
    go("home");
    press("查看全部");
    expect($("#layer .modal")).toBeTruthy();
    key("Escape");
    expect($("#layer .modal")).toBeNull();
  });
});
