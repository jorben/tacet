# 实施前工程验证基线

源码基线：`fc2ffa69844396aa62e29ed8b849e199d7eaa6d7`。本轮检查在引入 M0 业务代码之前执行；结果用于区分既有行为与新增实现，不证明后端功能已实现。

环境：Apple M4 / arm64，16 GiB RAM，macOS 27.2（26B123），Go 1.26.1，uv 0.13.1，Node 26.8.2，npm 11.19.1。

执行命令：

```sh
PATH="$HOME/go/bin:$PATH" wails3 task check
npm --prefix frontend test
npm --prefix frontend run build
PATH="$HOME/go/bin:$PATH" wails3 task build:cli
```

结果：全部退出码 0。`check` 的 Go vet、前端类型检查、Go 测试（含架构导入限制）与原 Python 7 项检查通过；前端 12 个文件、168 项测试通过；Vite 生产构建与 CLI 构建通过。浏览器测试出现 Wails 环境提示；npm 提示项目 minimum-release-age 配置未来不再支持，未导致失败。

CLI 业务命令仍为 not implemented，Python 插件没有格式处理能力，Wails 只有 AppInfo/窗口操作。因此这里的全部绿色只表示骨架和现有前端测试通过，不能作为真实后端闭环证据。

新增 M0 原型检查、资源实验和对应限制记录在单独结果中，不将后续执行追记成“实施前”结果。此次没有运行桌面打包、Windows 实机、Office/WPS、检测质量和目标用户验收。
