import { describe, expect, it, vi } from "vitest";
import { act, render, waitFor } from "@testing-library/react";
import App from "../App";
import { getState, store } from "../store";
import { setService } from "../services";
import type { TacetService } from "../services";

// Production shape: no preloaded demo data, window chrome follows the host.
describe("生产形态外壳", () => {
  it("不预置演示任务，窗口关闭走宿主", async () => {
    const quit = vi.fn(async () => {});
    const minimise = vi.fn(async () => {});
    const hide = vi.fn(async () => {});
    let closeHandler: (() => void) | null = null;
    let quitHandler: (() => void) | null = null;
    const host: TacetService = {
      host: "darwin",
      appInfo: async () => ({ name: "Tacet", version: "9.9.9" }),
      minimiseWindow: minimise,
      hideWindow: hide,
      quitApp: quit,
      onQuitRequested: (h) => {
        quitHandler = h;
        return () => {};
      },
      onCloseRequested: (h) => {
        closeHandler = h;
        return () => {};
      },
    };
    setService(host);
    await act(async () => {
      render(<App />);
    });
    // test builds carry the scene navigator, which loads lazily; let it settle before the test ends
    await waitFor(() => expect(document.querySelector("#proto")).toBeTruthy());

    expect(document.documentElement.dataset.host).toBe("darwin");
    expect(document.querySelector("#win")!.getAttribute("data-host")).toBe("darwin");
    expect(document.querySelector(".lights")).toBeNull(); // the OS draws the traffic lights
    expect(getState().update.current).toBe("9.9.9");
    expect(document.querySelector("#win")!.getAttribute("data-version")).toBe("9.9.9");
    expect(document.querySelectorAll(".home-history .hrow")).toHaveLength(0);

    // tray mode: the window is hidden to the tray, the application keeps running
    expect(closeHandler).toBeTypeOf("function");
    act(() => closeHandler!());
    expect(hide).toHaveBeenCalledTimes(1);
    expect(minimise).not.toHaveBeenCalled();
    expect(quit).not.toHaveBeenCalled();

    // “退出” in the tray with unfinished work: asked once, not quit at once
    act(() => store.update((d) => { d.history.unshift({ id: "p1", k: "batch", name: "x", meta: "", st: "stop", stl: "", when: "", park: true } as never); }));
    act(() => quitHandler!());
    expect(quit).not.toHaveBeenCalled();
    await waitFor(() => expect(document.querySelector(".modal, [role=dialog]")).toBeTruthy());
    act(() => store.update((d) => { d.history = []; }));
    // with nothing unfinished it quits
    act(() => quitHandler!());
    expect(quit).toHaveBeenCalledTimes(1);
    quit.mockClear();

    // quit mode with nothing running: end the application through the host
    act(() => store.update((d) => { d.settings.closeAct = "quit"; }));
    act(() => closeHandler!());
    expect(quit).toHaveBeenCalledTimes(1);
    setService(null);
  });
});
