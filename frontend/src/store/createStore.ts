// A tiny observable store: immutable state, updates written as mutations of an immer draft, and a hook that
// subscribes React components to a selected slice.
import { enableMapSet, produce, type Draft } from "immer";
import { useCallback, useRef, useSyncExternalStore } from "react";

enableMapSet();

export interface Store<T> {
  get(): T;
  /** Apply a recipe to a draft of the state; subscribers are notified once if anything changed. */
  update(recipe: (draft: Draft<T>) => void): void;
  /** Replace the whole state (used to reset between tests and scenes). */
  replace(next: T): void;
  subscribe(listener: () => void): () => void;
}

export function createStore<T>(initial: T): Store<T> {
  let state = initial;
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach((l) => l());
  return {
    get: () => state,
    update(recipe) {
      const next = produce(state, recipe);
      if (next === state) return;
      state = next;
      notify();
    },
    replace(next) {
      state = next;
      notify();
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}

/** Subscribe to `selector(state)`. The selector must return a stable value (a primitive or part of the state) unless `isEqual` says two results are the same. */
export function useStoreSelector<T, U>(store: Store<T>, selector: (state: T) => U, isEqual: (a: U, b: U) => boolean = Object.is): U {
  const cache = useRef<{ value: U } | null>(null);
  const getSnapshot = useCallback(() => {
    const next = selector(store.get());
    if (cache.current && isEqual(cache.current.value, next)) return cache.current.value;
    cache.current = { value: next };
    return next;
  }, [store, selector, isEqual]);
  return useSyncExternalStore(store.subscribe, getSnapshot, getSnapshot);
}

export function shallowEqual<T>(a: T, b: T): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || !a || !b) return false;
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  return ka.length === kb.length && ka.every((k) => Object.is((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
}
