import { expect, it } from "vitest";
import { intervalsStartedBy } from "./intervals";

// The runtime is imported while the setup files load (setup.ts reaches it through the services). Its environment
// poll must be one of the intervals cleared when a file ends, or on a slow runner it fires after jsdom is torn down
// and vitest fails the run with "window is not defined" (#16).
it("the Wails runtime's environment poll is cleared when a test file ends", () => {
  expect(intervalsStartedBy("@wailsio/runtime")).toBeGreaterThan(0);
});
