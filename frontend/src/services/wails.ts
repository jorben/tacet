import { Application, Events, System, Window } from "@wailsio/runtime";
import { AppInfo, CloseHandlerReady } from "../../bindings/github.com/jorben/tacet/internal/gui/service";
import type { HostKind, TacetService } from "./types";

/** Must match internal/gui/window.go EventCloseRequested. */
export const EVENT_CLOSE_REQUESTED = "tacet:close-requested";

function hostKind(): HostKind {
  if (System.IsMac()) return "darwin";
  if (System.IsWindows()) return "windows";
  return "linux";
}

/** Adapter over the Wails runtime and the existing internal/gui binding. */
export const wailsService: TacetService = {
  get host() {
    return hostKind();
  },
  async appInfo() {
    const info = await AppInfo();
    return { name: info.name, version: info.version };
  },
  async minimiseWindow() {
    await Window.Minimise();
  },
  async quitApp() {
    await Application.Quit();
  },
  onCloseRequested(handler) {
    const off = Events.On(EVENT_CLOSE_REQUESTED, () => handler());
    // Tell the host a handler exists; before this the native close behaviour stays.
    void CloseHandlerReady();
    return off;
  },
};
