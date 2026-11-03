import { defineConfig } from "vitest/config";

export default defineConfig({
  define: { __SCENES__: true },
  test: {
    environment: "jsdom",
    // intervals.ts first: it must see the intervals the runtime starts while setup.ts imports it (#16)
    setupFiles: ["./src/test/intervals.ts", "./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    css: false,
  },
});
