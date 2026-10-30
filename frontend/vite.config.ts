import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig(({ command, mode }) => ({
  define: {
    // Scene navigator + fixtures are a dev/test aid (see src/dev); production builds drop them unless VITE_SCENES=1.
    __SCENES__: JSON.stringify(command === "serve" || mode === "test" || process.env.VITE_SCENES === "1"),
  },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), wails("./bindings")],
}));
