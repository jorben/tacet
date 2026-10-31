// Pure operations on the document of a single-file task: creating it, hit and image decisions, and derived values.
import { buildDocument } from "./sampleDocument";
import { outputName, sizeStr } from "./format";
import type {
  BatchSummary, CategoryId, DocImage, Hit, Single, Treatment, ViewMode,
} from "./types";

export interface NewSingleDefaults {
  mode: ViewMode;
  preset: string;
  modelId: string | null;
}

export function createSingle(name: string, ext: string, size: number | undefined, defaults: NewSingleDefaults, extra: Partial<Single> = {}): Single {
  return {
    name, ext, size: sizeStr(size || 1887436), phase: "processing", stage: 0, sp: 0, mode: defaults.mode, focus: -1, catAct: {},
    preset: defaults.preset, modelId: defaults.modelId, out: "桌面 / 已脱敏", sameAll: true, from: null, page: 0, runId: 0,
    ...buildDocument(),
    ...extra,
  };
}

export const treatmentOf = (s: Pick<Single, "catAct">, t: CategoryId): Treatment => s.catAct[t] || "mask";

/** Ids of the hits in reading order. */
export function orderedHitIds(s: Pick<Single, "pages">): number[] {
  const ids: number[] = [];
  s.pages.forEach((pg) => pg.forEach((b) => (b.segs || []).forEach((g) => { if (g.h != null) ids.push(g.h); })));
  return ids;
}

export interface CategoryStat {
  redacted: number;
  kept: number;
  /** Low-confidence hits not looked at yet. */
  unseen: number;
}
export function categoryStats(s: Pick<Single, "hits">): Partial<Record<CategoryId, CategoryStat>> {
  const m: Partial<Record<CategoryId, CategoryStat>> = {};
  s.hits.forEach((h) => {
    const o = (m[h.type] ||= { redacted: 0, kept: 0, unseen: 0 });
    if (h.state === "redact") o.redacted++;
    else o.kept++;
    if (h.low && !h.seen) o.unseen++;
  });
  return m;
}

export const unseenLowHits = (s: Pick<Single, "hits">): Hit[] => s.hits.filter((h) => h.low && !h.seen);
export const redactedCount = (s: Pick<Single, "hits">): number => s.hits.filter((h) => h.state === "redact").length;
export const keptImageCount = (s: Pick<Single, "imgs">): number => s.imgs.filter((i) => i.act === "keep").length;
/** Keeping an image means the output is a partial result. */
export const isPartialResult = (s: Pick<Single, "imgs">): boolean => keptImageCount(s) > 0;

export function imageSummary(s: Pick<Single, "imgs">, verb: string): string {
  const n = s.imgs.length;
  const k = keptImageCount(s);
  return !k ? verb : k === n ? "保留" : "保留 " + k + " · " + verb + " " + (n - k);
}

/** Hits with the same text and category as `h` (the “同值一起” group). */
export const sameValueHits = (s: Pick<Single, "hits">, h: Hit): Hit[] => s.hits.filter((x) => x.text === h.text && x.type === h.type);

export interface ManualSelection {
  /** `page.block.segment` of the text run the selection lies in. */
  k: string;
  st: number;
  en: number;
  text: string;
}

/** Turns a selected piece of plain text into a manual hit and splits the text run around it. */
export function addManualHit(s: Single, sel: ManualSelection, type: CategoryId): void {
  const [pi, bi, si] = sel.k.split(".").map(Number) as [number, number, number];
  const b = s.pages[pi]![bi]!;
  const g = b.segs![si] as { t: string };
  const h: Hit = {
    id: s.hits.length, type, text: sel.text, low: false, seen: true, src: "人工添加", state: "redact",
    page: pi, para: b.at.para, sec: b.at.sec, manual: true,
  };
  s.hits.push(h);
  const parts: Single["pages"][number][number]["segs"] = [];
  if (sel.st > 0) parts!.push({ t: g.t.slice(0, sel.st) });
  parts!.push({ h: h.id });
  if (sel.en < g.t.length) parts!.push({ t: g.t.slice(sel.en) });
  b.segs!.splice(si, 1, ...parts!);
}

/** The manifest of a single file: one row, shaped like a batch so the same page can show it. */
export function singleManifest(s: Single): BatchSummary {
  const partial = isPartialResult(s);
  const man = s.hits.some((h) => h.manual || h.state === "keep") || partial || Object.keys(s.catAct || {}).length > 0;
  return {
    title: s.name, n: 1, out: s.out, phase: "result", manifestFilter: s.manifestFilter, manifestLimit: s.manifestLimit,
    files: [{
      i: 0, id: "T0001", ext: s.ext, outputName: s.live ? s.live.output : outputName(s.name, s.ext), st: partial ? "partial" : "done", man,
      ...(s.live ? { sha: s.live.digest ?? "" } : {}),
    }],
  };
}

export function setImageActions(imgs: DocImage[], act: DocImage["act"]): void {
  imgs.forEach((i) => { i.act = act; });
}

