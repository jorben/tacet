// Service boundary between the UI and the (not yet implemented) Go kernel.
// The UI only talks to this interface; today everything except appInfo is simulated by the mock service.

export interface AppInfo {
  name: string;
  version: string;
}

export interface TacetService {
  /** Product name and version (the one capability that exists in internal/gui today). */
  appInfo(): Promise<AppInfo>;
  /** True while the capability is simulated by the prototype engine, never a real operation. */
  readonly simulated: boolean;
}
