import type { DocLocation } from "./types";
// Small formatting helpers for numbers, file names and document locations.

export const fmt = (n: unknown): string => Number(n).toLocaleString("zh-CN");

export function extOf(name: string): string {
  const m = /\.([a-z0-9]+)$/i.exec(name);
  return m ? m[1]!.toLowerCase() : "pdf";
}

export function sizeStr(bytes: number): string {
  return bytes > 1048576 ? (bytes / 1048576).toFixed(1) + " MB" : Math.max(1, Math.round(bytes / 1024)) + " KB";
}

/**
 * DOCX has no fixed pages: the page comes from saved page-break hints (approximate), so headings and
 * paragraph numbers carry the location.
 */
export function locStr(ext: string, page: number | null, sec: string, para: number, short = false): string {
  if (ext !== "docx") return "第 " + page + " 页";
  return [
    page && !short ? "约第 " + page + " 页" : "",
    sec ? (short ? sec.split(/\s/)[0] : sec) : "",
    "第 " + para + " 段",
  ]
    .filter(Boolean)
    .join(" · ");
}

/** locStr for a located hit or block: a live DOCX shows its approximate page only when it has one. */
export function locAt(ext: string, at: DocLocation, short = false): string {
  const page = at.hint !== undefined ? at.hint || null : at.page + 1;
  const where = locStr(ext, page, at.sec, at.para, short);
  if (!at.cell || ext !== "docx") return where;
  const parts = where.split(" · ");
  parts.splice(parts.length - 1, 0, at.cell);
  return parts.join(" · ");
}

export interface OutputKind {
  /** Short description of the output format. */
  l: string;
  tip: string;
  verb: string;
}
export function outputKind(ext: string): OutputKind {
  if (ext === "pdf") return { l: "PDF · 栅格化", tip: "不可搜索 · 无链接与表单 · 200 DPI", verb: "涂抹" };
  if (ext === "docx") return { l: "DOCX · 保留版式", tip: "图片换为同尺寸占位图", verb: "涂抹" };
  return { l: ext.toUpperCase() + " · 同格式", tip: "保持编码与换行", verb: "涂抹" };
}

export function outputName(name: string, ext: string): string {
  return name.replace(/\.[^.]+$/, "") + "_已脱敏." + ext;
}

/** Reads a numeric prefix such as “1,200 份”. */
export const parseCount = (s: string): number => parseInt(s.replace(/,/g, "")) || 0;
