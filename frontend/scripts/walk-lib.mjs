// Shared helpers for the headless-browser walkthrough (Playwright).
import { chromium } from "playwright-core";
import { existsSync, readdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export function findChromium() {
  if (process.env.CHROMIUM_PATH) return process.env.CHROMIUM_PATH;
  const root = join(homedir(), "Library/Caches/ms-playwright");
  if (!existsSync(root)) return undefined;
  const dirs = readdirSync(root).filter((d) => d.startsWith("chromium-")).sort().reverse();
  for (const d of dirs) {
    const p = join(root, d, "chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing");
    if (existsSync(p)) return p;
  }
  return undefined;
}

export async function launch() {
  return chromium.launch({ executablePath: findChromium(), headless: true });
}
