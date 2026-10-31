import { describe, expect, it } from "vitest";
import {
  advanceFiles, batchStatus, beginRun, countFiles, createBatch, etaLabel, fakeHash, finishProcessing, groupMembers, groupTotal,
  isSettled, keepDays, machineDone, matchesFileFilter, openGroups, pendingItems, publishReady, resolveGroup, undoResolution,
} from "../batch";
import { demoNames } from "../../dev/scenes";

const make = (n = 214) => createBatch(demoNames(n), { title: "测试", preset: "通用办公脱敏", modelId: "astr" });
/** Runs the simulated clock until nothing is queued or processing. */
function drain(b: ReturnType<typeof make>, max = 3000) {
  for (let i = 0; i < max && b.files.some((f) => f.st === "queue" || f.st === "proc"); i++) advanceFiles(b);
}

describe("createBatch", () => {
  it("相同文件名列表得到相同批次", () => {
    const a = make(60);
    const b = make(60);
    expect(JSON.stringify(a)).toBe(JSON.stringify(b));
  });

  it("不支持的格式与超限 PDF 在预检被拒绝，并写明原因", () => {
    const b = make();
    const bad = b.files.filter((f) => f.st === "bad");
    expect(bad.length).toBe(3);
    expect(bad.map((f) => f.reason).join()).toMatch(/格式不支持/);
    expect(bad.map((f) => f.reason).join()).toMatch(/超过 2,000 页 · 实际 2,340 页/);
    expect(bad.map((f) => f.reason).join()).toMatch(/文件已损坏/);
  });

  it("小批次只有截图与疑似身份证两类例外", () => {
    const b = createBatch(["a.pdf", "b.docx", "c.txt"], { preset: "x", modelId: null });
    expect(b.groups.map((g) => g.id).sort()).toEqual(["fail", "idck", "shot"].sort());
  });

  it("大批次的例外分组覆盖密码、扫描页、截图、嵌入、转曲", () => {
    const ids = make().groups.map((g) => g.id);
    for (const id of ["pwd", "fail", "scan", "shot", "idck", "embed", "curve"]) expect(ids).toContain(id);
  });

  it("文件按 T0001 编号", () => {
    expect(make(12).files.map((f) => f.id).slice(0, 2)).toEqual(["T0001", "T0002"]);
  });
});

describe("批次运行", () => {
  it("预检阶段状态为待开始，开始后进入运行", () => {
    const b = make(40);
    expect(batchStatus(b)).toEqual(["idle", "待开始"]);
    beginRun(b);
    expect(b.phase).toBe("run");
  });

  it("同时处理的文件不超过 10 份", () => {
    const b = make();
    beginRun(b);
    advanceFiles(b);
    expect(countFiles(b.files).proc).toBeLessThanOrEqual(10);
    expect(countFiles(b.files).proc).toBeGreaterThan(0);
  });

  it("暂停后不再派发新文件，进行中的会完成", () => {
    const b = make();
    beginRun(b);
    advanceFiles(b);
    b.paused = true;
    const queued = countFiles(b.files).queue;
    for (let i = 0; i < 20; i++) advanceFiles(b);
    expect(countFiles(b.files).queue).toBe(queued);
    expect(countFiles(b.files).proc).toBe(0);
  });

  it("引导方式：有例外的文件等待处理；无人值守：搁置不阻塞", () => {
    const guided = make();
    beginRun(guided);
    drain(guided);
    expect(countFiles(guided.files).wait).toBeGreaterThan(0);
    expect(countFiles(guided.files).held).toBe(0);
    const auto = make();
    auto.mode = "auto";
    beginRun(auto);
    drain(auto);
    expect(countFiles(auto.files).held).toBeGreaterThan(0);
    expect(countFiles(auto.files).wait).toBe(0);
  });

  it("严格方式：每份文件都进入“逐份确认”，且不能整组处理", () => {
    const b = make(30);
    b.mode = "strict";
    beginRun(b);
    const g = b.groups.find((x) => x.id === "strict")!;
    expect(g.noBulk).toBe(true);
    expect(Object.keys(g.per)).toHaveLength(b.files.filter((f) => f.st !== "bad").length);
  });

  it("验证后自动导出：通过验证的文件稍后自动写出", () => {
    const b = make(30);
    b.autoPub = true;
    beginRun(b);
    for (let i = 0; i < 200; i++) advanceFiles(b);
    expect(countFiles(b.files).done + countFiles(b.files).partial).toBeGreaterThan(0);
  });

  it("关闭自动导出时停在“待导出”，手动导出全部写出", () => {
    const b = make(30);
    b.autoPub = false;
    beginRun(b);
    for (let i = 0; i < 200; i++) advanceFiles(b);
    const ready = countFiles(b.files).ready;
    expect(ready).toBeGreaterThan(0);
    expect(publishReady(b)).toBe(ready);
    expect(countFiles(b.files).ready).toBe(0);
  });

  it("验证未完成会自动重试，超过两次转为整份失败", () => {
    const b = make(30);
    beginRun(b);
    const f = b.files.find((x) => x.st !== "bad" && !x.g.length && !x.late.length && !x.vr && !x.res && !x.brk)!;
    f.det = false;
    f.vr = 3;
    for (let i = 0; i < 4; i++) {
      finishProcessing(b, f);
      if (f.st === "proc") f.t = 0;
    }
    expect(f.failWhy).toContain("验证超时");
    expect(f.g).toContain("fail");
  });

  it("残留会自动扩大涂抹范围并累计", () => {
    const b = make(30);
    beginRun(b);
    const f = b.files.find((x) => x.st !== "bad" && !x.g.length && !x.late.length && !x.vr && !x.res && !x.brk)!;
    f.res = true;
    f.resN = 2;
    finishProcessing(b, f);
    expect(b.widened).toBe(2);
    expect(f.reason).toBe("扩大涂抹范围");
  });

  it("重新处理后仍失败的文件标为无法处理", () => {
    const b = make(30);
    beginRun(b);
    const f = b.files.find((x) => x.st !== "bad" && !x.g.length && !x.late.length && !x.vr && !x.res && !x.brk)!;
    f.brk = true;
    f.redo = true;
    finishProcessing(b, f);
    expect(f.st).toBe("bad");
    expect(f.reason).toBe("重新处理后仍失败");
  });
});

describe("例外分组", () => {
  const settle = () => {
    const b = make();
    beginRun(b);
    drain(b);
    return b;
  };

  it("成员、数量与待处理总数一致", () => {
    const b = settle();
    const total = openGroups(b).reduce((a, g) => a + groupTotal(g, groupMembers(b, g)), 0);
    expect(pendingItems(b)).toBe(total);
    expect(total).toBeGreaterThan(0);
  });

  it("整组处理后文件回到处理，处理记录可撤销", () => {
    const b = settle();
    const g = openGroups(b).find((x) => x.id === "shot")!;
    const members = groupMembers(b, g);
    const res = resolveGroup(b, g.id, "do")!;
    expect(res.n).toBeGreaterThan(0);
    expect(g.done).toHaveLength(1);
    members.forEach((fi) => expect(b.files[fi]!.g).not.toContain(g.id));
    expect(undoResolution(b, res)).toBe(true);
    expect(g.done).toHaveLength(0);
    members.forEach((fi) => expect(b.files[fi]!.g).toContain(g.id));
  });

  it("“保留”缺口类例外会导出为部分结果", () => {
    const b = settle();
    const g = openGroups(b).find((x) => x.gap)!;
    const members = groupMembers(b, g);
    resolveGroup(b, g.id, "keep");
    members.forEach((fi) => expect(b.files[fi]!.partial).toBe(true));
  });

  it("“跳过”的文件不再导出，也不再属于任何分组", () => {
    const b = settle();
    const g = openGroups(b)[0]!;
    const fi = groupMembers(b, g)[0]!;
    resolveGroup(b, g.id, "skip", [fi]);
    expect(b.files[fi]!.st).toBe("skipped");
    expect(b.files[fi]!.g).toEqual([]);
  });

  it("已导出的文件不能撤销", () => {
    const b = settle();
    const g = openGroups(b).find((x) => x.id === "embed")!;
    const res = resolveGroup(b, g.id, "do")!;
    res.restore.files.forEach(({ fi }) => { b.files[fi]!.st = "done"; });
    expect(undoResolution(b, res)).toBe(false);
  });
});

describe("状态与统计", () => {
  it("machineDone / isSettled", () => {
    const b = make(30);
    beginRun(b);
    expect(machineDone(b)).toBe(false);
    expect(isSettled(b)).toBe(false);
  });

  it("状态文案随进度变化", () => {
    const b = make(30);
    beginRun(b);
    advanceFiles(b);
    expect(batchStatus(b)[0]).toBe("run");
    drain(b);
    expect(batchStatus(b)[0]).toBe("warn");
    expect(batchStatus(b)[1]).toMatch(/待你处理 \d+ 处/);
  });

  it("剩余时间估计", () => {
    expect(etaLabel({ ...countFiles([]), queue: 0, proc: 0 })).toBe("");
    expect(etaLabel({ ...countFiles([]), queue: 300, proc: 0 })).toMatch(/^剩余约 \d+–\d+ 分钟$/);
  });

  it("文件筛选与搜索（名称或编号）", () => {
    const f = { name: "简历-张三.pdf", id: "T0007", st: "done" as const };
    expect(matchesFileFilter(f, "done", "")).toBe(true);
    expect(matchesFileFilter(f, "bad", "")).toBe(false);
    expect(matchesFileFilter(f, "all", "张三")).toBe(true);
    expect(matchesFileFilter(f, "all", "t0007")).toBe(true);
    expect(matchesFileFilter(f, "all", "李四")).toBe(false);
    expect(matchesFileFilter({ ...f, st: "queue" }, "run", "")).toBe(true);
    expect(matchesFileFilter({ ...f, st: "held" }, "wait", "")).toBe(true);
    expect(matchesFileFilter({ ...f, st: "skipped" }, "bad", "")).toBe(true);
  });

  it("保留天数默认 7 天", () => {
    expect(keepDays({ keep: "30" })).toBe("30");
    expect(keepDays({ keep: "0" })).toBe("7");
    expect(keepDays(null)).toBe("7");
  });

  it("清单中的校验值稳定", () => {
    expect(fakeHash(3)).toBe(fakeHash(3));
    expect(fakeHash(3)).not.toBe(fakeHash(4));
    expect(fakeHash(0)).toMatch(/…$/);
  });
});
