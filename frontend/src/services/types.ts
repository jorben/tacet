// Service boundary between the UI and the (not yet implemented) Go kernel.
// The UI only talks to this interface; today everything except the host/window calls is simulated.

export interface AppInfo {
  name: string;
  version: string;
}

/** Operating-system chrome the window is drawn with. */
export type HostKind = "browser" | "darwin" | "windows" | "linux";

export interface TacetService {
  /** Product name and version (the one data capability that exists in internal/gui today). */
  appInfo(): Promise<AppInfo>;
  /** Where the UI runs: a plain browser (dev, tests) or a Wails desktop host. */
  readonly host: HostKind;
  /** Minimise the main window. No-op in a browser. */
  minimiseWindow(): Promise<void>;
  /** End the application. No-op in a browser. */
  quitApp(): Promise<void>;
  /**
   * Called once the UI is ready to handle user-initiated window closes (title-bar button, Cmd+W).
   * The handler is invoked instead of closing the window; return value is ignored.
   * Returns a function that removes the handler.
   */
  onCloseRequested(handler: () => void): () => void;
}
