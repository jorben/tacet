// Needs a build that includes the scene navigator: VITE_SCENES=1 npm run build && npx vite preview --port 4173
// Headless-browser walkthrough: visits every prototype scene and the main flows, fails on page errors,
// and writes screenshots plus docs/walkthrough.md. Usage: npm run build && npx vite preview & node scripts/walkthrough.mjs
import { mkdirSync, writeFileSync } from "node:fs";
import { launch } from "./walk-lib.mjs";

const BASE = process.env.BASE_URL || "http://127.0.0.1:4173/";
const OUT = new URL("../docs/walkthrough/", import.meta.url).pathname;
mkdirSync(OUT, { recursive: true });

const browser = await launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 920 } });
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => { if (m.type() === "error" && !/favicon|404/.test(m.text())) errors.push("console: " + m.text()); });

const rows = [];
let n = 0;
async function shot(name, expects) {
  await page.waitForTimeout(250);
  const text = (await page.locator("#win").innerText()).replace(/\s+/g, " ");
  const miss = expects.filter((e) => !text.includes(e));
  const file = String(++n).padStart(2, "0") + "-" + name + ".png";
  await page.screenshot({ path: OUT + file });
  rows.push({ name, file, ok: miss.length === 0, miss });
  if (miss.length) errors.push(name + ": 缺少文案 " + miss.join(" | "));
}
async function scene(k, name, expects) {
  await page.goto(BASE + "#scene=" + k);
  await page.reload();
  await page.waitForTimeout(k === "s3" ? 2600 : 400);
  await shot(name, expects);
}

// scenes (prototype navigator)
await scene("home", "首页", ["拖入文档", "通用办公脱敏", "最近", "季度客户材料"]);
await scene("fresh", "首页 · 首次使用", ["拖入文档", "请先下载隐私识别模型"]);
await scene("model-download", "模型 · 下载中", ["正在准备识别模型，可先选择文档。", "查看进度"]);
await scene("model-paused", "模型 · 已暂停", ["模型下载已暂停", "继续准备"]);
await scene("model-offline", "模型 · 断网", ["模型准备需要处理", "查看并处理"]);
await scene("model-disk", "模型 · 空间不足", ["模型准备需要处理", "查看并处理"]);
await scene("model-integrity", "模型 · 校验失败", ["模型准备需要处理", "查看并处理"]);
await scene("model-loading", "模型 · 准备中", ["正在准备识别模型，可先选择文档。"]);
await scene("model-load", "模型 · 启动失败", ["模型准备需要处理", "查看并处理"]);
await scene("model-wait", "模型 · 已选文件等待", ["劳动合同.pdf", "尚未开始检测", "继续处理"]);
await scene("model-batch", "模型 · 批量未就绪", ["批次 · 12 份", "准备模型后处理"]);
await scene("model-plugins", "插件 · 模型待下载", ["插件", "模型", "AstrLink Guard", "下载模型"]);
await scene("s1", "单份 · 检测中", ["劳动合同.pdf", "读取", "检测"]);
await scene("s2", "单份 · 复核", ["确认并导出", "原文", "效果", "对比", "命中"]);
await scene("s5", "单份 · DOCX 复核", ["劳动合同.docx", "约 1 / 3"]);
await scene("s4", "单份 · 左右对比", ["原文", "效果 · 预览"]);
await scene("s3", "单份 · 完成", ["已导出", "打开所在位置", "查看清单"]);
await scene("b1", "批量 · 预检", ["214", "引导", "无人值守", "严格", "开始处理"]);
await scene("b2", "批量 · 处理中", ["已导出", "待你处理", "自动处理", "文件"]);
await scene("b5", "批量 · 人工复核", ["复核队列", "请决策"].filter((x) => x !== "复核队列"));
await scene("b3", "批量 · 完成", ["已导出", "查看清单"]);
await scene("b6", "批量 · 无人值守完成", ["已导出"]);
await scene("b4", "批量 · 导出清单", ["SHA-256", "全部", "未导出"]);
await scene("presets", "方案", ["通用办公脱敏", "检测方式", "最低置信度", "规则集"]);
await scene("plugins", "插件", ["插件", "第三方", "OFD"]);
await scene("settings", "设置", ["外观", "涂抹样式", "存储", "版本", "关于"]);
await scene("close", "关闭窗口", ["最小化到托盘"]);

// flows
await page.goto(BASE + "#scene=home"); await page.reload(); await page.waitForTimeout(400);
await page.locator('[data-a="allHistory"]').click();
await shot("最近任务对话框", ["最近任务", "季度客户材料"]);
await page.keyboard.press("Escape");
await page.locator('[data-a="homePreset"]').click();
await shot("方案菜单", ["简历脱敏", "日志清理", "管理方案"]);
await page.keyboard.press("Escape");

await page.goto(BASE + "#scene=presets"); await page.reload(); await page.waitForTimeout(300);
await page.locator('[data-a="ptab"][data-v="rules"]').click();
await shot("规则集", ["中国场景规则集", "手机号", "企业词条"]);
await page.locator('[data-a="rsel"][data-v="corp"]').click();
await page.locator('[data-a="radd"]').click();
await shot("添加规则对话框", ["添加规则", "词表", "正则", "命中归类"]);
await page.keyboard.press("Escape");
await page.locator('[data-a="pimp"]').click().catch(() => {});

await page.goto(BASE + "#scene=plugins"); await page.reload(); await page.waitForTimeout(300);
await page.locator('[data-a="plinstall"]').click();
await shot("安装插件", ["安装插件", "Git 仓库", "本地文件夹"]);
await page.locator('[data-a="instNext"]').click();
await shot("安装插件 · 审查", ["EML 邮件", "插件可读取文档原文", "安装"]);
await page.locator('[data-a="instDo"]').click();
await shot("安装插件 · 完成", ["EML 邮件"]);

await page.goto(BASE + "#scene=settings"); await page.reload(); await page.waitForTimeout(300);
await page.locator('[data-a="checkUpd"]').click();
await page.waitForTimeout(1200);
await shot("检查更新", ["发现新版本"]);
await page.locator('[data-a="updateApp"]').click();
await shot("更新对话框", ["更新至", "更新功能尚未接入"]);
await page.keyboard.press("Escape");
await page.locator('[data-a="licenses"]').click();
await shot("开源许可", ["开源许可", "Apache"]);
await page.keyboard.press("Escape");
await page.locator('[data-a="theme"][data-v="dark"]').click();
await shot("深色外观", ["外观"]);
await page.locator('[data-a="theme"][data-v="light"]').click();

// batch main flow: precheck -> run -> review decide -> result -> manifest
await page.goto(BASE + "#scene=b1"); await page.reload(); await page.waitForTimeout(300);
await page.locator('[data-a="startBatch"]').click();
await page.waitForTimeout(6000);
await shot("批量 · 运行一段时间", ["已导出"]);
if (await page.locator('[data-a="review"]').count()) {
  await page.locator('button[data-a="review"]').first().click();
  await shot("批量 · 进入复核", ["请决策"]);
  await page.locator('[data-a="rvDo"]').first().click();
  await shot("批量 · 复核决策后", ["已决定"]);
}

writeFileSync(
  new URL("../docs/walkthrough.md", import.meta.url).pathname,
  "# 无头浏览器走查记录\n\n由 `scripts/walkthrough.mjs` 生成（Playwright + Chromium，1440×920）。每行对应一张截图（`docs/walkthrough/`），并断言关键中文文案存在、页面无脚本错误。\n\n| # | 页面 / 流程 | 结果 | 截图 |\n| --- | --- | --- | --- |\n" +
    rows.map((r, i) => "| " + (i + 1) + " | " + r.name + " | " + (r.ok ? "通过" : "缺失：" + r.miss.join("、")) + " | `" + r.file + "` |").join("\n") +
    "\n\n页面错误：" + (errors.length ? "\n\n" + errors.map((e) => "- " + e).join("\n") : "无") + "\n",
);
await browser.close();
console.log(rows.map((r) => (r.ok ? "OK  " : "FAIL ") + r.name + (r.ok ? "" : " -> " + r.miss.join("|"))).join("\n"));
console.log("errors:", errors);
process.exit(errors.length ? 1 : 0);
