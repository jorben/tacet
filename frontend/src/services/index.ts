import { mockService } from "./mock";
import { wailsService } from "./wails";
import type { TacetService } from "./types";

export type { AppInfo, HostKind, TacetService } from "./types";

let current: TacetService | null = null;

/** A real Wails host exposes a native message channel; a plain browser (dev, tests, walkthrough) does not. */
export function inWailsHost(): boolean {
  const w = window as any;
  return !!(w.chrome?.webview?.postMessage || w.webkit?.messageHandlers?.external?.postMessage);
}

export function getService(): TacetService {
  if (!current) current = typeof window !== "undefined" && inWailsHost() ? wailsService : mockService;
  return current;
}

export function setService(s: TacetService | null) {
  current = s;
}
