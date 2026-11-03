import { describe, expect, it } from "vitest";
import { place } from "../overlay/Popover";

/** A layer of `h` px and a popover of natural height `ph` px; the anchor's rect is given in layer coordinates. */
function setup(h: number, ph: number, anchor: { top: number; bottom: number }) {
  const layer = document.createElement("div");
  const el = document.createElement("div");
  layer.append(el);
  layer.getBoundingClientRect = () => ({ left: 0, top: 0, right: 800, bottom: h, width: 800, height: h, x: 0, y: 0, toJSON: () => ({}) });
  Object.defineProperty(el, "offsetWidth", { get: () => 200 });
  // the natural height while uncapped; a capped one after
  Object.defineProperty(el, "offsetHeight", { get: () => (el.style.maxHeight ? Math.min(ph, parseFloat(el.style.maxHeight)) : ph) });
  const rect = new DOMRect(100, anchor.top, 80, anchor.bottom - anchor.top);
  place(el, { anchor: rect });
  return { top: parseFloat(el.style.top), maxHeight: el.style.maxHeight, overflow: el.style.overflowY };
}

describe("弹层定位", () => {
  it("下方放得下时放在下方，不限高", () => {
    expect(setup(800, 200, { top: 100, bottom: 130 })).toEqual({ top: 136, maxHeight: "", overflow: "" });
  });
  it("只有上方放得下时翻到上方", () => {
    expect(setup(800, 300, { top: 600, bottom: 630 })).toEqual({ top: 294, maxHeight: "", overflow: "" });
  });
  it("上下都放不下时放在空间大的一侧并限高滚动，不超出视口", () => {
    const below = setup(800, 700, { top: 200, bottom: 230 });
    expect(below.top).toBe(236);
    expect(below.top + parseFloat(below.maxHeight)).toBe(800 - 8);
    expect(below.overflow).toBe("auto");
    const above = setup(800, 700, { top: 560, bottom: 590 });
    expect(above.top).toBe(8);
    expect(above.top + parseFloat(above.maxHeight)).toBe(560 - 6);
  });
  it("锚点在视口下方以外时（行被滚出视野）菜单仍完整落在视口内", () => {
    const r = setup(800, 400, { top: 900, bottom: 930 });
    expect(r.top).toBeGreaterThanOrEqual(8);
    expect(r.top + 400).toBeLessThanOrEqual(800 - 8);
  });
});
