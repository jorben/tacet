import { describe, expect, it } from "vitest";
import { produce } from "immer";
import { advanceFiles, beginRun, createBatch, isExported } from "../batch";
import { currentItem, decide, enterReview, reviewExportSummary, reviewLocation, reviewWhere, selectItem, stepItem, syncReview, undoDecision } from "../review";
import { demoNames } from "../../dev/scenes";
import type { Batch } from "../types";

function reviewing(): Batch {
  const b = createBatch(demoNames(214), { preset: "办公外发", modelId: "astr" });
  beginRun(b);
  for (let i = 0; i < 400; i++) advanceFiles(b);
  return produce(b, (d) => { enterReview(d as Batch, null); });
}
const run = (b: Batch, recipe: (d: Batch) => void) => produce(b, (d) => { recipe(d as Batch); });
const itemOf = (b: Batch, gid: string) => b.rv!.items.find((x) => x.gid === gid && !x.dec)!;

describe("复核队列", () => {
  it("按分组、文件、页码排序，进入时停在第一条待决策项", () => {
    const b = reviewing();
    const gis = b.rv!.items.map((x) => x.gi);
    expect([...gis].sort((a, c) => a - c)).toEqual(gis);
    expect(currentItem(b)!.dec).toBeNull();
    expect(b.phase).toBe("review");
  });

  it("上一条 / 下一条循环切换", () => {
    let b = reviewing();
    const first = b.rv!.cur;
    b = run(b, (d) => stepItem(d, -1));
    expect(b.rv!.cur).toBe(b.rv!.items.at(-1)!.key);
    b = run(b, (d) => stepItem(d, 1));
    expect(b.rv!.cur).toBe(first);
  });

  it("没有变化时 syncReview 保持队列引用不变", () => {
    const b = reviewing();
    const items = b.rv!.items;
    const next = produce(b, (d) => { expect(syncReview(d as Batch)).toBe(false); });
    expect(next.rv!.items).toBe(items);
  });
});

describe("复核决策", () => {
  it("决策第一项：记录进度，自动跳到下一条待决策项", () => {
    let b = reviewing();
    const cur = currentItem(b)!;
    b = run(b, (d) => { decide(d, d.groups[cur.gi]!.acts[0]!.k); });
    expect(b.rv!.items.find((x) => x.key === cur.key)!.dec).toBe(b.groups[cur.gi]!.acts[0]!.k);
    expect(currentItem(b)!.key).not.toBe(cur.key);
    expect(b.groups[cur.gi]!.done.find((d) => d.rv)!.n).toBe(1);
  });

  it("“本组其余一起”一次决定同组所有待处理项", () => {
    let b = reviewing();
    const g = b.groups.find((x) => !x.noBulk && x.id !== "pwd" && b.rv!.items.filter((i) => i.gid === x.id).length > 1)!;
    const first = b.rv!.items.find((x) => x.gid === g.id)!;
    b = run(b, (d) => { selectItem(d, first.key); d.rv!.all = true; decide(d, g.acts[0]!.k); });
    expect(b.rv!.items.filter((x) => x.gid === g.id).every((x) => x.dec === g.acts[0]!.k)).toBe(true);
    expect(b.rv!.all).toBe(false);
  });

  it("改决定：旧记录扣减，新记录累加；已导出后不能改", () => {
    let b = reviewing();
    const g = b.groups.find((x) => x.id === "embed")!;
    const it = itemOf(b, "embed");
    b = run(b, (d) => { selectItem(d, it.key); decide(d, "do"); });
    b = run(b, (d) => { selectItem(d, it.key); decide(d, "keep"); });
    const recs = b.groups.find((x) => x.id === g.id)!.done.filter((d) => d.rv);
    expect(recs.map((r) => r.k)).toEqual(["keep"]);
    b = run(b, (d) => { d.files[it.fi]!.st = "done"; });
    let msg: unknown;
    run(b, (d) => { selectItem(d, it.key); msg = decide(d, "do"); });
    expect(msg).toEqual({ ok: false, toast: "已导出，不能更改" });
  });

  it("跳过会把文件标为已跳过，改回后重新处理", () => {
    let b = reviewing();
    // a file with a single item in the group: deciding it settles the group for that file
    const it = b.rv!.items.find((x) => x.gid === "embed" && !x.dec && b.groups[x.gi]!.per[x.fi] === 1)!;
    b = run(b, (d) => { selectItem(d, it.key); decide(d, "skip"); });
    expect(b.files[it.fi]!.st).toBe("skipped");
    b = run(b, (d) => { selectItem(d, it.key); decide(d, "do"); });
    expect(b.files[it.fi]!.st).not.toBe("skipped");
  });

  it("撤销恢复决定、文件状态与处理记录", () => {
    let b = reviewing();
    const it = currentItem(b)!;
    const before = JSON.stringify(b.groups.map((g) => g.done));
    b = run(b, (d) => { decide(d, d.groups[it.gi]!.acts[0]!.k); });
    b = run(b, (d) => { undoDecision(d); });
    expect(b.rv!.items.find((x) => x.key === it.key)!.dec).toBeNull();
    expect(JSON.stringify(b.groups.map((g) => g.done))).toBe(before);
    expect(b.rv!.cur).toBe(it.key);
  });

  it("没有可撤销的决定时什么也不做", () => {
    const b = reviewing();
    expect(produce(b, (d) => { expect(undoDecision(d as Batch)).toBeNull(); })).toBe(b);
  });

  it("全部决定后复核完成，当前项为空", () => {
    let b = reviewing();
    for (let i = 0; i < 500 && currentItem(b); i++) {
      const it = currentItem(b)!;
      b = run(b, (d) => { decide(d, d.groups[it.gi]!.acts[0]!.k); });
    }
    expect(currentItem(b)).toBeNull();
    expect(b.rv!.fin).toBe(true);
    expect(b.rv!.items.every((x) => x.dec)).toBe(true);
  });

  it("复核完成后的后台导出进度", () => {
    let b = reviewing();
    const it = currentItem(b)!;
    b = run(b, (d) => { decide(d, d.groups[it.gi]!.acts[0]!.k); });
    const x = reviewExportSummary(b);
    expect(x.total).toBeGreaterThan(0);
    expect(x.pending).toBeGreaterThan(0);
    expect(x.autoPublish).toBe(true);
    expect(isExported("done")).toBe(true);
  });
});

describe("位置说明", () => {
  it("DOCX 用约页码或章节与段落，其他格式用页码", () => {
    expect(reviewLocation({ ext: "pdf", name: "a.pdf", i: 0 }, { p: 3, j: 0 })).toBe("第 3 页");
    expect(reviewLocation({ ext: "docx", name: "劳动合同-1.docx", i: 1 }, { p: 2, j: 0 })).toMatch(/约第 2 页 · 第.条 · 第 \d+ 段/);
    // every fourth DOCX was saved without page-break hints
    expect(reviewLocation({ ext: "docx", name: "简历-1.docx", i: 4 }, { p: 2, j: 0 })).not.toContain("页");
  });

  it("按分组说明“在哪里”", () => {
    const b = reviewing();
    const g = (id: string) => b.groups.find((x) => x.id === id)!;
    const f = b.files.find((x) => x.ext === "pdf")!;
    const it = { key: "k", gid: "x", gi: 0, fi: f.i, j: 0, p: 2, dec: null };
    expect(reviewWhere(g("pwd"), f, it)).toBe("已加密");
    expect(reviewWhere(g("fail"), f, it)).toBe("整份文件");
    expect(reviewWhere(g("scan"), f, it)).toBe(`全部 ${f.pages} 页`);
  });
});
