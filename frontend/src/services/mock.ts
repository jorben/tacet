import type { TacetService } from "./types";

/** Browser/test host: nothing native exists, window operations are no-ops. */
export const mockService: TacetService = {
  host: "browser",
  async appInfo() {
    return { name: "Tacet", version: "0.0.0-dev" };
  },
  async minimiseWindow() {},
  async hideWindow() {},
  async quitApp() {},
  onQuitRequested() {
    return () => {};
  },
  onCloseRequested() {
    return () => {};
  },
};
