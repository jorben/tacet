import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SettingsPage } from "../features/settings/SettingsPage";
import { overlayStore } from "../overlay/store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import { store } from "../store";
import { FakeEngine } from "./liveFake";

afterEach(() => { cleanup(); setService(null); });

describe("工作区设置", () => {
  it("显示实际 GiB 占用和可恢复任务数，打开当前工作区目录", async () => {
    const engine = new FakeEngine();
    setService({ ...mockService, engine });
    store.update((d) => {
      d.history = [{ id: "h1", k: "file", name: "文档", meta: "", st: "keep", stl: "已暂停", when: "刚刚" }];
    });
    render(<SettingsPage />);
    await screen.findByText("加密保护中 · 2.5 GiB · 1 可恢复任务");
    fireEvent.click(screen.getByRole("button", { name: "打开目录" }));
    await waitFor(() => expect(engine.calls).toContain("openWorkspace"));
    fireEvent.click(screen.getByRole("button", { name: "清理…" }));
    expect(overlayStore.get().modal?.type).toBe("cleanWorkspace");
  });

  it("打开期间禁用按钮，失败后恢复并给出提示", async () => {
    const engine = new FakeEngine();
    let rejectOpen!: (e: Error) => void;
    engine.openWorkspace = vi.fn(() => new Promise<void>((_, reject) => { rejectOpen = reject; }));
    setService({ ...mockService, engine });
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("button", { name: "打开目录" }));
    expect(screen.getByRole("button", { name: "正在打开…" })).toBeDisabled();
    rejectOpen(new Error("OPEN_DIRECTORY_FAILED"));
    await waitFor(() => expect(screen.getByRole("button", { name: "打开目录" })).toBeEnabled());
    expect(overlayStore.get().toast?.msg).toBe("无法打开工作区目录，请稍后重试");
  });

  it("容量读取失败时不展示演示容量", async () => {
    const engine = new FakeEngine();
    engine.workspaceFails = new Error("WORKSPACE_UNAVAILABLE");
    setService({ ...mockService, engine });
    render(<SettingsPage />);
    await screen.findByText("加密保护中 · 容量暂不可用 · 0 可恢复任务");
  });
});
