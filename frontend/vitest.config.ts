import { defineConfig } from "vitest/config";

export default defineConfig({
  define: { __SCENES__: true },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    css: false,
  },
});
