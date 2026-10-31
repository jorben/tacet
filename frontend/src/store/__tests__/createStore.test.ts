import { describe, expect, it, vi } from "vitest";
import { createStore, shallowEqual } from "../createStore";

describe("createStore", () => {
  it("更新不可变：旧状态不变，未改动的部分保持引用", () => {
    const store = createStore({ a: { n: 1 }, b: { n: 2 } });
    const before = store.get();
    store.update((d) => { d.a.n = 5; });
    expect(before.a.n).toBe(1);
    expect(store.get().a.n).toBe(5);
    expect(store.get().b).toBe(before.b);
  });

  it("没有实际改动时不通知", () => {
    const store = createStore({ n: 1 });
    const listener = vi.fn();
    store.subscribe(listener);
    store.update((d) => { d.n = 1; });
    store.update(() => {});
    expect(listener).not.toHaveBeenCalled();
    store.update((d) => { d.n = 2; });
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("可取消订阅；replace 整体替换", () => {
    const store = createStore({ n: 1 });
    const listener = vi.fn();
    const off = store.subscribe(listener);
    store.replace({ n: 9 });
    expect(listener).toHaveBeenCalledTimes(1);
    off();
    store.replace({ n: 10 });
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.get().n).toBe(10);
  });

  it("支持 Map 与 Set", () => {
    const store = createStore({ m: new Map([["a", 1]]) });
    store.update((d) => { d.m.set("b", 2); });
    expect([...store.get().m.keys()]).toEqual(["a", "b"]);
  });

  it("shallowEqual", () => {
    expect(shallowEqual({ a: 1 }, { a: 1 })).toBe(true);
    expect(shallowEqual({ a: 1 }, { a: 2 })).toBe(false);
    expect(shallowEqual({ a: 1 }, { a: 1, b: 2 })).toBe(false);
    expect(shallowEqual(1 as any, 1 as any)).toBe(true);
    expect(shallowEqual(null as any, {} as any)).toBe(false);
  });
});
