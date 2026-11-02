<!-- reviewed-through: 5a7bfd8 (2026-11-02 12:10 +0800) -->
# 经验记录

这里记录已合并的工作出过什么错，以及能提前抓住它的规则。改代码前先读。

每条的格式是：先写规则，再写原因和证据（提交号、CI 运行号），最后写“出现”，即在几个不同的日子里发现过。同一条规则在 3 个不同的日子里出现，就把它移进 [AGENTS.md](AGENTS.md) 的验收要求，并在这里留一行指向它。

什么时候写、怎样定期复盘，见 [AGENTS.md 的“维护 LESSONS.md”](AGENTS.md#维护-lessonsmd)。第一行的 `reviewed-through` 标记记录定期复盘做到了 master 的哪个提交。标记从 5a7bfd8 开始：现有两条来自 2026-11-02 的 Windows CI 修复和 CI 跳过问题，在那之前的提交没有系统地复盘过。

## 验证

**交叉编译不等于在 Windows 上测过。**
- `GOOS=windows go vet` 和 `go build` 不运行测试，平台行为差异只有在 Windows 上实际运行才会暴露。
- 2026-11-02 之前，验证报告对 Windows 的记录只有一句“仅 `GOOS=windows go vet` 交叉检查”。c4dfc3b 让 CI 第一次在 `windows-latest` 上跑完整测试套件，结果当天连续修了 7 个只在 Windows 上出现的缺陷：
  - 0923453：`url.URL` 把 `C:\...` 拼成 `file://C:/...`，所有加密数据库都打不开；
  - d1ea236：`os.OpenFile` 不带 `FILE_SHARE_DELETE`，仍被打开的资源文件删不掉；
  - 84626fa：临时文件还开着就删除，Windows 拒绝；
  - 2ff7d4f：`/usr/bin/python3` 在 Windows 上不算绝对路径，绕过了相对路径检查；
  - 76360af、1bd8df7：测试假设了 POSIX 权限位，只读文件在 Windows 上是 0444；
  - 4d72b3d：Windows 上的 Python 按 ANSI 代码页读中文设计文档。
- 规则：Windows 相关的结论以 CI 的 Windows job 或真机运行为准；只做过交叉编译，就写“未在 Windows 上运行”。
- 出现：1 次（2026-11-02）

**CI 绿了不代表测试跑过：缺依赖的测试会静默跳过。**
- CI 的 `go` job 没有安装 `python/.venv`，跨语言测试在 macOS 上全部 `t.Skip`。运行 41384722770 的 macOS job 里，`internal/app` 只用了 0.72s，`internal/validate`、`internal/cli`、`internal/detect`、`internal/extract` 都是 `(cached)`。装好环境以后，PR #3 的同一个 job 里 `internal/app` 用了 417s（带 `-race`），本机不带 `-race` 是 79s。
- 其中 16 个测试文件带 `//go:build darwin`，Windows 上根本不编译，所以 Windows job 的耗时说明不了跳过与否：最初的分析拿它当证据，推断错了（2026-11-02 的研究报告）。没有构建标签的只有 `internal/host/session_test.go`，它写死的 `bin/python` 在 Windows 上找不到。
- 规则：测试需要的环境由 CI 装好，再用开关（`TACET_REQUIRE_PLUGINS=1`）把“缺少依赖”变成失败。判断 CI 是否真的跑过，要看耗时和跳过数，不能只看颜色；某个平台上跑得快，先确认测试在那个平台上是否被编译（构建标签），再下“跳过了”的结论。CI 结果不能来自测试缓存（`-count=1`）。
- 修复：ci/verification-gates（`testenv.Python`、`testenv.Missing`、CI 安装 uv 环境）。
- 出现：1 次（2026-11-02）
