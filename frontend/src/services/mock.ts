import type { TacetService } from "./types";

/** Prototype behaviour: model download, install, update and export are all simulated in the UI engine. */
export const mockService: TacetService = {
  simulated: true,
  async appInfo() {
    return { name: "Tacet", version: "1.0.0" };
  },
};
