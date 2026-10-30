import { beforeAll, describe, expect, it } from "vitest";
import { $, $$, clickA, go, mountApp, win } from "./helpers";
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
    for (const [v, title] of [["presets", "方案"], ["plugins", "插件"], ["settings", "设置"], ["home", "Tacet"]] as const) {
      clickA("go", v);
      expect($("#tbTitle")!.textContent).toContain(title);
      expect($(`#rail [data-v="${v}"]`)!.getAttribute("aria-current")).toBe("page");
    }
  });

  it("默认走 mock 服务，不伪造为真实现", async () => {
    const s = getService();
    expect(s.simulated).toBe(true);
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
    clickA("closeWin");
    expect($("#layer .toast")!.textContent).toContain("已最小化到托盘");
    go("settings");
    clickA("closeAct", "quit");
    go("b1");
    clickA("closeWin");
    expect($("#layer .modal")!.textContent).toContain("有未完成的任务");
    expect($("#layer .modal")!.textContent).toContain("保存并关闭");
    expect($("#layer .modal")!.textContent).toContain("停止并清理");
  });
});
