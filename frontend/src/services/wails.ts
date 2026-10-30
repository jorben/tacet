import { Service } from "../../bindings/github.com/jorben/tacet/internal/gui";
import type { TacetService } from "./types";

/** Adapter over the existing Wails binding (internal/gui). Only AppInfo exists; nothing else is faked as real. */
export const wailsService: TacetService = {
  simulated: true,
  async appInfo() {
    const info = await Service.AppInfo();
    return { name: info.name, version: info.version };
  },
};
