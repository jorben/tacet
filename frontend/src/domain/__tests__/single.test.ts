import { describe, expect, it } from "vitest";
import { buildDocument } from "../sampleDocument";
import { extOf, fmt, locStr, outputKind, outputName, parseCount, sizeStr } from "../format";
import { addManualHit, categoryStats, createSingle, imageSummary, isPartialResult, orderedHitIds, sameValueHits, singleManifest, treatmentOf } from "../single";
import { createRng, pick, randInt, reseed } from "../random";

const doc = () => createSingle("劳动合同.pdf", "pdf", 1887436, { mode: "orig", preset: "办公外发", modelId: "astr" });

describe("示例文档", () => {
  it("三页，命中带类别、位置与来源，页内含证件照与公章", () => {
    const d = buildDocument();
    expect(d.pages).toHaveLength(3);
    expect(d.hits.length).toBeGreaterThanOrEqual(15);
    expect(d.imgs.map((i) => i.label)).toEqual(["证件照", "公章"]);
    expect(d.hits[0]).toMatchObject({ type: "org", text: "杭州澄音科技有限公司", state: "redact", page: 0 });
    expect(d.hits.filter((h) => h.low).every((h) => h.src === "模型 · 低置信")).toBe(true);
  });

  it("段落序号连续，章节标题随后续段落继承", () => {
    const d = buildDocument();
    const withSec = d.hits.find((h) => h.sec.startsWith("第三条"))!;
    expect(withSec.sec).toBe("第三条　劳动报酬");
    const paras = d.hits.map((h) => h.para);
    expect([...paras].sort((a, b) => a - b)).toEqual(paras);
  });

  it("每次得到独立的数据", () => {
    const a = buildDocument();
    a.hits[0]!.state = "keep";
    expect(buildDocument().hits[0]!.state).toBe("redact");
  });
});

describe("单份文档操作", () => {
  it("命中按阅读顺序排列", () => {
    const s = doc();
    expect(orderedHitIds(s)).toHaveLength(s.hits.length);
    expect(orderedHitIds(s)[0]).toBe(0);
  });

  it("类别统计：涂抹与保留数，未看的低置信项", () => {
    const s = doc();
    s.hits.find((h) => h.type === "name")!.state = "keep";
    const st = categoryStats(s);
    expect(st.name!.kept).toBe(1);
    expect(st.name!.redacted + st.name!.kept).toBe(s.hits.filter((h) => h.type === "name").length);
    expect(st.name!.unseen).toBeGreaterThan(0);
  });

  it("处理方式默认涂抹", () => {
    const s = doc();
    expect(treatmentOf(s, "phone")).toBe("mask");
    s.catAct.phone = "ph";
    expect(treatmentOf(s, "phone")).toBe("ph");
  });

  it("保留图像即部分结果；图像摘要", () => {
    const s = doc();
    expect(isPartialResult(s)).toBe(false);
    expect(imageSummary(s, "涂抹")).toBe("涂抹");
    s.imgs[0]!.act = "keep";
    expect(isPartialResult(s)).toBe(true);
    expect(imageSummary(s, "涂抹")).toBe("保留 1 · 涂抹 1");
    s.imgs[1]!.act = "keep";
    expect(imageSummary(s, "涂抹")).toBe("保留");
  });

  it("同值命中", () => {
    const s = doc();
    const h = s.hits.find((x) => s.hits.filter((y) => y.text === x.text && y.type === x.type).length > 1)!;
    expect(sameValueHits(s, h).length).toBeGreaterThan(1);
  });

  it("补充涂抹：拆分文字并新增人工命中", () => {
    const s = doc();
    const k = (() => {
      for (let pi = 0; pi < s.pages.length; pi++)
        for (let bi = 0; bi < s.pages[pi]!.length; bi++)
          for (let si = 0; si < (s.pages[pi]![bi]!.segs || []).length; si++) {
            const g = s.pages[pi]![bi]!.segs![si]!;
            if (g.t && g.t.length > 12) return { key: `${pi}.${bi}.${si}`, text: g.t, pi, bi, si };
          }
      throw new Error("no text run");
    })();
    const n = s.hits.length;
    const before = s.pages[k.pi]![k.bi]!.segs!.length;
    addManualHit(s, { k: k.key, st: 3, en: 7, text: k.text.slice(3, 7) }, "phone");
    expect(s.hits).toHaveLength(n + 1);
    expect(s.hits.at(-1)).toMatchObject({ manual: true, type: "phone", text: k.text.slice(3, 7), src: "人工添加", state: "redact" });
    expect(s.pages[k.pi]![k.bi]!.segs!.length).toBe(before + 2);
    expect(s.pages[k.pi]![k.bi]!.segs!.map((g) => g.t ?? "⟨hit⟩").join("")).toBe(k.text.slice(0, 3) + "⟨hit⟩" + k.text.slice(7) + "");
  });

  it("单份的清单只有一行：人工修改过则标注“自动 + 人工”", () => {
    const s = doc();
    expect(singleManifest(s).files[0]).toMatchObject({ id: "T0001", st: "done", man: false, outputName: "劳动合同_已脱敏.pdf" });
    s.hits[0]!.state = "keep";
    expect(singleManifest(s).files[0]!.man).toBe(true);
    s.imgs[0]!.act = "keep";
    expect(singleManifest(s).files[0]!.st).toBe("partial");
  });
});

describe("格式与随机数", () => {
  it("扩展名、大小、数字", () => {
    expect(extOf("a.PDF")).toBe("pdf");
    expect(extOf("noext")).toBe("pdf");
    expect(sizeStr(500)).toBe("1 KB");
    expect(sizeStr(2 * 1048576)).toBe("2.0 MB");
    expect(fmt(1234567)).toBe("1,234,567");
    expect(parseCount("1,200 份")).toBe(1200);
  });

  it("输出格式与文件名", () => {
    expect(outputKind("pdf").l).toBe("PDF · 栅格化");
    expect(outputKind("docx").l).toBe("DOCX · 保留版式");
    expect(outputKind("csv").l).toBe("CSV · 同格式");
    expect(outputName("合同.v2.docx", "docx")).toBe("合同.v2_已脱敏.docx");
  });

  it("DOCX 没有固定页码：先写章节与段落", () => {
    expect(locStr("pdf", 2, "", 1)).toBe("第 2 页");
    expect(locStr("docx", 2, "第一条 期限", 5)).toBe("约第 2 页 · 第一条 期限 · 第 5 段");
    expect(locStr("docx", null, "第一条 期限", 5)).toBe("第一条 期限 · 第 5 段");
    expect(locStr("docx", 2, "第一条 期限", 5, true)).toBe("第一条 · 第 5 段");
  });

  it("种子相同序列相同", () => {
    const a = createRng(42);
    const b = createRng(42);
    expect([a(), a(), a()]).toEqual([b(), b(), b()]);
    reseed(5);
    const x = [randInt(1, 100), pick([1, 2, 3])];
    reseed(5);
    expect([randInt(1, 100), pick([1, 2, 3])]).toEqual(x);
  });
});
