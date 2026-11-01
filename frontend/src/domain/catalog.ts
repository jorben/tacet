// Fixed vocabulary of the product: categories, labels and the exception groups a batch can raise.
import type { Boundary, CategoryId, FileStatus, GroupAction, ModelErrorKind, ModelStatus, OfficeCategoryId } from "./types";

export const CATEGORIES: Record<CategoryId, { l: string; ph: string }> = {
  org: { l: "公司名", ph: "公司" },
  name: { l: "人名", ph: "姓名" },
  phone: { l: "电话", ph: "电话" },
  email: { l: "邮箱", ph: "邮箱" },
  id: { l: "身份证", ph: "证件号" },
  addr: { l: "地址", ph: "地址" },
  card: { l: "银行卡", ph: "卡号" },
  date: { l: "日期", ph: "日期" },
  other: { l: "其他", ph: "已隐去" },
};
export const CATEGORY_IDS = Object.keys(CATEGORIES) as CategoryId[];
/** Office categories used in detection statistics. */
export const OFFICE_CATEGORIES: OfficeCategoryId[] = ["org", "name", "phone", "email", "id", "addr", "card", "date"];
/** Categories offered by schemes; the built-in models have no organization label. */
export const PRESET_CATEGORIES = OFFICE_CATEGORIES.filter((c) => c !== "org");
/** Order of the category list in the single-file review sidebar. */
export const REVIEW_CATEGORY_ORDER: CategoryId[] = [...OFFICE_CATEGORIES, "other"];
/** Categories offered when a selection is masked by hand. */
export const MANUAL_CATEGORIES: CategoryId[] = ["org", "name", "phone", "id", "addr", "other"];

export const HIT_SOURCE: Record<CategoryId, string> = {
  org: "模型 · 词表",
  name: "模型",
  addr: "模型",
  date: "模型",
  phone: "规则",
  email: "规则",
  id: "规则 · 校验",
  card: "规则 · 校验",
  other: "人工",
};

export const BOUNDARY_LABELS: Record<Exclude<Boundary, "">, string> = { digit: "数字", word: "字母或数字" };

export const MODEL_LABELS: Record<ModelStatus, string> = {
  missing: "待下载",
  downloading: "下载中",
  paused: "已暂停",
  verifying: "校验中",
  loading: "准备中",
  ready: "已就绪",
  error: "需处理",
};
/** [title, description, action label] */
export const MODEL_ERRORS: Record<ModelErrorKind, [string, string, string]> = {
  offline: ["下载已中断", "网络连接不可用，已下载的部分会保留。连接网络后继续下载。", "继续下载"],
  disk: ["可用空间不足", "请先释放磁盘空间，再重试。安装需要的空间包含下载文件、展开文件与临时副本。", "检查空间并重试"],
  integrity: ["模型文件校验未通过", "文件不完整或与当前应用版本不匹配，尚未加载。请重新下载此模型。", "重新下载"],
  load: ["模型未能启动", "文件校验已通过，但本地试运行失败。请关闭占用内存较多的应用后重试；仍失败时可重新下载。", "重新准备"],
};

/** [label, pill variant] */
export const FILE_STATUS: Record<FileStatus, [string, string]> = {
  queue: ["排队", ""],
  proc: ["处理中", "pri"],
  wait: ["待你处理", "warn"],
  held: ["待你处理", "warn"],
  ready: ["待导出", "pri"],
  done: ["已导出", "ok"],
  partial: ["部分结果", "ok"],
  bad: ["无法处理", "bad"],
  skipped: ["已跳过", "ghost"],
};

/** Formats the engine can read; anything else fails the precheck. */
export const SUPPORTED_EXTENSIONS = ["pdf", "docx", "txt", "md", "log", "csv", "jsonl", "json"];

/** Definition of an exception group; `perRange` is how many items one member file contributes. */
export interface GroupDefinition {
  id: string;
  ic: string;
  t: string;
  unit: string;
  block?: boolean;
  gap?: boolean;
  /** Share of the batch that lands in the group, at least `min` files. */
  frac?: number;
  min?: number;
  ext?: string;
  perRange?: [number, number];
  /** Share of files in which output validation finds one more item. */
  post?: number;
  /** Filled in after processing (whole-file failures), not at precheck. */
  after?: boolean;
  acts: GroupAction[];
}
export const GROUP_DEFINITIONS: GroupDefinition[] = [
  { id: "pwd", ic: "lock", t: "需要密码", unit: "份", block: true, frac: 0.01, min: 1, acts: [{ l: "解锁", k: "pwd" }, { l: "跳过此文件", k: "skip", n: "不导出" }] },
  { id: "fail", ic: "retry", t: "处理失败", unit: "份", block: true, after: true, acts: [{ l: "重新处理", k: "redo" }, { l: "跳过此文件", k: "skip", n: "不导出" }] },
  { id: "scan", ic: "scan", t: "扫描页，无文本层", unit: "份", ext: "pdf", frac: 0.014, min: 1, gap: true, acts: [{ l: "涂抹", k: "do", n: "整页" }, { l: "保留", k: "keep", n: "部分结果" }] },
  { id: "shot", ic: "image", t: "截图中含文字", unit: "处", frac: 0.056, perRange: [2, 5], gap: true, acts: [{ l: "涂抹", k: "do", n: "整张截图" }, { l: "保留", k: "keep", n: "部分结果" }] },
  { id: "idck", ic: "hash", t: "疑似身份证号 · 校验位不符", unit: "处", frac: 0.042, perRange: [1, 4], post: 0.01, acts: [{ l: "涂抹", k: "do", n: "整个号码" }, { l: "保留", k: "keep" }] },
  { id: "embed", ic: "clip", t: "嵌入文件", unit: "个", frac: 0.028, perRange: [1, 2], gap: true, acts: [{ l: "涂抹", k: "do", n: "整个附件" }, { l: "保留", k: "keep", n: "部分结果" }] },
  { id: "curve", ic: "pen", t: "疑似文字转曲", unit: "处", ext: "pdf", frac: 0.014, perRange: [1, 3], acts: [{ l: "涂抹", k: "do", n: "该区域" }, { l: "保留", k: "keep" }] },
];
/** The strict mode adds this group: every file needs an explicit confirmation, never in bulk. */
export const STRICT_GROUP_ID = "strict";

/** Groups that have no document preview in the review screen. */
export const NO_DOCUMENT_GROUPS = ["pwd", "fail", STRICT_GROUP_ID];
export const REVIEW_SOURCE: Record<string, string> = {
  strict: "严格方式 · 每份由你确认后才导出",
  idck: "格式像身份证号，但末位校验码不符",
  shot: "截图 · 可能含文字",
  embed: "嵌入文件 · 内容未检查",
  curve: "路径图形 · 疑似文字",
  scan: "整页图像 · 无文本层",
  pwd: "文件已加密",
};

/* Demo data used to synthesise batches */
export const SURNAMES = "王李张刘陈杨黄赵吴周徐孙马朱胡郭何高林罗".split("");
export const GIVEN_NAMES = ["雨桐", "子轩", "思远", "欣怡", "浩然", "佳琪", "俊杰", "梓涵", "一鸣", "诗雨", "嘉怡", "宇航", "若溪", "明哲"];
export const DEMO_FILE_KINDS: [string, string][] = [
  ["简历-{n}", "pdf"], ["简历-{n}", "docx"], ["劳动合同-{n}", "docx"], ["采购合同-2026-{k}", "pdf"],
  ["客户回访-{k}", "docx"], ["报价单-{k}", "pdf"], ["入职登记-{n}", "docx"], ["付款明细-{k}", "pdf"],
];
export const DOCUMENT_TITLES: Record<string, string> = {
  简历: "个人简历", 劳动合同: "劳动合同", 采购合同: "采购合同", 客户回访: "客户回访记录", 报价单: "报价单", 入职登记: "入职登记表", 付款明细: "付款明细",
};
export const FILLER_SENTENCES = [
  "双方应本着诚实信用原则履行约定的各项义务，未尽事宜另行协商。",
  "相关资料仅限本次业务使用，未经书面同意不得向第三方披露。",
  "如有变更，应提前以书面形式通知对方，并经双方确认后生效。",
  "附件与正文具有同等效力，二者不一致的以正文为准。",
  "本记录由经办人整理，经复核后归档，保存期限按公司制度执行。",
];

/** Numerals used for the clause headings of demo contracts (第一条 … 第九条). */
export const CLAUSE_NUMERALS = "一二三四五六七八九";
