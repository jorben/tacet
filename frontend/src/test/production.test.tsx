import { describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import App from "../App";
import { start } from "../engine";
import { S } from "../engine/state";
import { setService } from "../services";
import type { TacetService } from "../services";

// Production shape: no scene navigator, no preloaded demo data, window chrome follows the host.
describe("生产形态外壳", () => {
  it("不挂载场景导航、不预置演示任务，窗口关闭走宿主", async () => {
    const quit = vi.fn(async () => {});
    const minimise = vi.fn(async () => {});
    let closeHandler: (() => void) | null = null;
    const host: TacetService = {
      host: "darwin",
      appInfo: async () => ({ name: "Tacet", version: "9.9.9" }),
      minimiseWindow: minimise,
      quitApp: quit,
      onCloseRequested: (h) => {
        closeHandler = h;
        return () => {};
      },
    };
    setService(host);
    // App enables scenes in test mode; render the shell and start the engine the way main.tsx does in production.
    render(<App />);
    await act(async () => {
      await start({ scenes: false });
    });

    expect(document.documentElement.dataset.host).toBe("darwin");
    expect(document.querySelector("#win")!.getAttribute("data-host")).toBe("darwin");
    expect(document.querySelector(".lights")).toBeNull(); // the OS draws the traffic lights
    expect(S.update.current).toBe("9.9.9");
    expect(document.querySelectorAll(".home-history .hrow")).toHaveLength(0);
    expect(document.body.textContent).not.toContain("原型");

    // tray mode: minimise through the host
    expect(closeHandler).toBeTypeOf("function");
    act(() => closeHandler!());
    expect(minimise).toHaveBeenCalledTimes(1);

    // quit mode with nothing running: end the application through the host
    S.set.closeAct = "quit";
    act(() => closeHandler!());
    expect(quit).toHaveBeenCalledTimes(1);
    setService(null);
  });
});
