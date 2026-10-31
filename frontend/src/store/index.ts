import { createStore, shallowEqual, useStoreSelector } from "./createStore";
import { createInitialState, type AppState } from "./state";

export type { AppState } from "./state";
export { shallowEqual } from "./createStore";

/** The single source of truth for application data. UI-only state (open menus, dialogs, toasts) lives in `overlay`. */
export const store = createStore<AppState>(createInitialState());

export function useAppState<U>(selector: (state: AppState) => U, isEqual?: (a: U, b: U) => boolean): U {
  return useStoreSelector(store, selector, isEqual);
}

/** Back to the state of a fresh installation (tests, demo scenes). */
export function resetStore(): void {
  store.replace(createInitialState());
}

export const getState = (): AppState => store.get();
export const useShallow = shallowEqual;
