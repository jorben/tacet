# 经验记录

这里记录已合并的工作出过什么错，以及能提前抓住它的规则。改代码前先读。

每条的格式是：先写规则，再写原因和证据（提交号、CI 运行号），最后写“出现”，即在几个不同的日子里发现过。同一条规则在 3 个不同的日子里出现，就把它移进 [AGENTS.md](AGENTS.md) 的验收要求，并在这里留一行指向它。

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
- CI 的 `go` job 没有安装 `python/.venv`，17 个跨语言测试文件全部 `t.Skip`。在运行 41384722770 的 Windows job 里，`internal/validate` 只用了 0.23s，`internal/cli` 只用了 1.9s；本机实跑分别是 19.1s 和 18.5s。macOS job 里 `internal/validate` 还是 `(cached)`。另外解释器路径写死成 `bin/python`，Windows 上就算装了环境也找不到。
- 规则：测试需要的环境由 CI 装好，再用开关（`TACET_REQUIRE_PLUGINS=1`）把“缺少依赖”变成失败。判断 CI 是否真的跑过，要看耗时和跳过数，不能只看颜色。CI 结果不能来自测试缓存（`-count=1`）。
- 修复：ci/verification-gates（`testenv.Python`、`testenv.Missing`、CI 安装 uv 环境）。
- 出现：1 次（2026-11-02）
