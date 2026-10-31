import { describe, expect, it } from "vitest";
import type { EngineFile, EngineUnit } from "../../services/types";
import { buildLiveDocument, decisionsOf, engineCategory, groupOf, liveGroups, reasonLabel, toBatchFile, uiCategory, unitOffsets, zeroHits } from "../live";
import type { BatchFile } from "../types";

const unit = (seq: number, text: string, findings: EngineUnit["findings"], part = "body"): EngineUnit => ({ seq, part, ordinal: seq, visibility: "visible", text, findings });
const find = (id: number, start: number, end: number, category = "phone", extra: object = {}) => ({ id, start, end, category, source: "rule", score: 1, low: false, ...extra });

describe("引擎文档 → 复核视图", () => {
  it("命中按 UTF-16 偏移切分，代理对（emoji）不错位", () => {
    const text = "😀张三 电话 13812345678";
    const doc = buildLiveDocument([unit(1, text, [find(7, 8, 19)])], "txt");
    const b = doc.pages[0]![0]!;
    expect(b.segs).toEqual([{ t: "😀张三 电话 " }, { h: 0 }]);
    expect(doc.hits[0]).toMatchObject({ text: "13812345678", type: "phone", state: "redact", src: "规则" });
    expect(doc.findingOf).toEqual([7]);
    expect(doc.seqOf).toEqual([[1]]);
  });

  it("按页部分归页；无页号的格式全部在第一页", () => {
    const doc = buildLiveDocument([unit(1, "a", [], "page/1"), unit(2, "b", [], "page/3"), unit(3, "c", [], "word/document.xml")], "pdf");
    expect(doc.pages.map((p) => p.length)).toEqual([2, 0, 1]);
    expect(doc.seqOf).toEqual([[1, 3], [], [2]]);
    expect(doc.hits[0]).toBeUndefined();
  });

  it("重叠的命中只显示先出现的一个，其余仍由引擎处理", () => {
    const doc = buildLiveDocument([unit(1, "0123456789", [find(1, 0, 6), find(2, 4, 9), find(3, 6, 9)])], "txt");
    expect(doc.findingOf).toEqual([1, 3]);
    expect(doc.pages[0]![0]!.segs).toEqual([{ h: 0 }, { h: 1 }, { t: "9" }]);
  });

  it("越界或空区间的命中被忽略，不会崩溃", () => {
    const doc = buildLiveDocument([unit(1, "abc", [find(1, 2, 2), find(2, 1, 99), find(3, -0, 1)])], "txt");
    expect(doc.findingOf).toEqual([3]);
  });

  it("引擎里的决定与上一次视图里的选择会带过来；人工候选默认已看", () => {
    const first = buildLiveDocument([unit(1, "x 13812345678 y", [find(5, 2, 13, "phone", { low: true })])], "txt");
    first.hits[0]!.state = "keep";
    first.hits[0]!.seen = true;
    const next = buildLiveDocument([unit(1, "x 13812345678 y 张三", [find(5, 2, 13, "phone", { low: true }), find(6, 16, 18, "person", { source: "manual" })])], "txt", { findingOf: first.findingOf, hits: first.hits });
    expect(next.hits[0]).toMatchObject({ state: "keep", seen: true, low: true, src: "规则 · 低置信" });
    expect(next.hits[1]).toMatchObject({ type: "name", manual: true, seen: true, state: "redact" });
    expect(decisionsOf(next.hits, next)).toEqual([{ id: 5, keep: true }, { id: 6, keep: false }]);
  });

  it("引擎已有的“保留”决定显示为保留", () => {
    const doc = buildLiveDocument([unit(1, "1 2", [find(1, 0, 1, "phone", { decision: "keep" }), find(2, 2, 3, "phone", { decision: "redact" })])], "txt");
    expect(doc.hits.map((h) => h.state)).toEqual(["keep", "redact"]);
  });

  it("隐藏文本单元以元数据样式显示；DOCX 用段落样式", () => {
    const hidden = { ...unit(1, "h", []), visibility: "hidden" };
    expect(buildLiveDocument([hidden], "docx").pages[0]![0]!.k).toBe("meta");
    expect(buildLiveDocument([unit(1, "p", [])], "docx").pages[0]![0]!.k).toBe("pi");
  });

  it("选区偏移 = 之前各段长度 + 段内偏移", () => {
    const doc = buildLiveDocument([unit(1, "ab13812345678cd", [find(1, 2, 13)])], "txt");
    const b = doc.pages[0]![0]!;
    expect(unitOffsets(b, doc.hits, 2, 0, 2)).toEqual([13, 15]);
    expect(unitOffsets(b, doc.hits, 0, 1, 2)).toEqual([1, 2]);
  });
});

describe("类别与原因", () => {
  it("两个方向的类别映射互逆，未知类别归入其他", () => {
    for (const c of ["phone", "email", "id", "card", "date", "name", "addr", "org"] as const) expect(uiCategory(engineCategory(c))).toBe(c);
    expect(uiCategory("secret")).toBe("other");
    expect(engineCategory("other")).toBe("other");
  });
  it("已知原因码有说明，未知的原样显示，空值为 null", () => {
    expect(reasonLabel("PASSWORD_INCORRECT")).toContain("密码");
    expect(reasonLabel("SOME_NEW_CODE")).toBe("SOME_NEW_CODE");
    expect(reasonLabel(undefined)).toBeNull();
  });
});

const row = (state: string, extra: Partial<EngineFile> = {}): EngineFile => ({ id: "j1", name: "f0001", state, status: "x", format: "txt", pending: 0, findings: 0, ...extra });
const base = (i: number): BatchFile => ({ i, id: "T000" + (i + 1), name: "a.txt", ext: "txt", pages: 0, h: zeroHits(), imgs: 0, st: "queue", t: 0, g: [], late: [], det: false, partial: false, man: false });

describe("批次行与例外组", () => {
  it("状态映射：完成/部分/待导出/待处理/失败/跳过/处理中", () => {
    const st = (s: string, e: Partial<EngineFile> = {}) => toBatchFile(base(0), row(s, e)).st;
    expect([st("completed"), st("partial"), st("ready_to_publish"), st("awaiting_review"), st("locked"), st("blocked"), st("failed"), st("unprocessable"), st("cancelled"), st("exporting"), st("preflighted")])
      .toEqual(["done", "partial", "ready", "held", "held", "held", "bad", "bad", "skipped", "proc", "queue"]);
  });

  it("类别计数落到办公类别，其他类别只计入命中总数之外", () => {
    const f = toBatchFile(base(0), row("completed", { categories: { phone: 2, id_number: 1, secret: 4 }, output: "f0001.txt", digest: "sha256:ab" }));
    expect(f.h.phone).toBe(2);
    expect(f.h.id).toBe(1);
    expect(f).toMatchObject({ outputName: "f0001.txt", sha: "sha256:ab", det: true });
  });

  it("失败文件带原因；等复核的文件不显示原因；清单里没有摘要时为空串而不是伪造值", () => {
    expect(toBatchFile(base(0), row("unprocessable", { detail: "FORMAT_NOT_SUPPORTED" })).reason).toBe("格式不支持");
    expect(toBatchFile(base(0), row("awaiting_review", { detail: "x" })).reason).toBeNull();
    expect(toBatchFile(base(0), row("completed")).sha).toBe("");
  });

  it("密码、复核、受阻分别进入各自的组", () => {
    expect(groupOf(row("blocked", { detail: "password_required" }))).toBe("live-password");
    expect(groupOf(row("locked"))).toBe("live-password");
    expect(groupOf(row("awaiting_review"))).toBe("live-review");
    expect(groupOf(row("blocked", { detail: "ACTIVITY_BUSY" }))).toBe("live-blocked");
    expect(groupOf(row("failed"))).toBe("live-blocked");
    expect(groupOf(row("completed"))).toBeNull();
    const rows = [row("awaiting_review", { pending: 3 }), row("completed"), row("locked"), row("awaiting_review", { pending: 0 })];
    const g = liveGroups(rows.map((_, i) => base(i)), rows);
    expect(g.map((x) => x.id)).toEqual(["live-review", "live-password"]);
    expect(g[0]!.per).toEqual({ 0: 3, 3: 1 });
    expect(g.every((x) => x.noBulk)).toBe(true);
  });
});
