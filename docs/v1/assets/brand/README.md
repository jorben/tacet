# Tacet 当前品牌与插画素材

本目录及相邻的 `../illustrations/` 仅保留当前终版素材与对应原稿。交互原型见 [tacet-prototype.html](../../tacet-prototype.html)。

## 幼苗角色 Logo

使用 Jorben 提供的原图，保留绿色背景、幼苗角色与构图，用于首页 Logo、标题栏图标和浏览器标签页图标。

| 文件 | 用途 |
| --- | --- |
| [tacet-seedling-logo.png](tacet-seedling-logo.png) | 用户提供的 1024 × 1024 PNG 原稿；也是桌面应用图标的唯一来源，构建时由 `build/Taskfile.yml` 的 `generate:icons` 生成 macOS `.icns` 与 Windows `.ico` |
| [tacet-seedling-logo.webp](tacet-seedling-logo.webp) | 256 × 256 WebP 界面素材，内嵌于原型的 `HOME_ICON` 及标签页图标 |

首页与标题栏使用相同比例的圆角处理。首页 Logo 显示为 80 × 80 CSS px。

## 首页底部脱敏流程插画

根据 Jorben 提供的设计稿，使用 MCP 生图工具（remote-magpie/codex/gpt-image-2）生成四阶段横向流程插画：文档导入、敏感信息识别、脱敏保护、结果输出。采用浅鼠尾草绿线稿、幼苗角色与轻薄植物背景，不嵌入文字。

| 文件 | 用途 |
| --- | --- |
| [tacet-redaction-flow-generated.png](../illustrations/tacet-redaction-flow-generated.png) | 2070 × 760 PNG 生成原稿 |
| [tacet-redaction-flow.webp](../illustrations/tacet-redaction-flow.webp) | 裁去上下空白并去除白底后的 2070 × 365 透明 WebP，内嵌于原型的 `HOME_FLOW` |

插画作为独立背景层固定在首页可视区域底部；前景内容在其上方独立滚动，模型引导、最近任务等内容的增减不会挤压或移动插画。使用空 `alt`、`aria-hidden` 和 `pointer-events: none`；浅色模式以低透明度呈现，深色模式调整亮度与饱和度，窄窗口等比缩小并保留完整流程。

## 维护约定

- PNG 原稿用于后续导出与调整，WebP 用于界面展示。
- 原型保持单 HTML 离线可用，图片以内嵌副本使用，无外部图片依赖。
- 替换素材时，同步更新对应 WebP 文件及 HTML 中的全部内嵌副本。
