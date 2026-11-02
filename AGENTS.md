# 给编码 agent 的说明

改代码前先读 [LESSONS.md](LESSONS.md)：近期合并的工作哪里出过错，以及能提前抓住它的规则。模块边界与设计依据见 [docs/v1](docs/v1)，模块间依赖由 `internal/archtest` 在 `go test` 中强制检查。

## 验收：一个改动合并前要拿出什么

说清楚跑了哪些检查、在哪个提交上跑的、输出是什么。**没跑的检查要写“没跑”，不能省略。**

1. **修复带一个在旧代码上失败的测试。** 把旧代码放回去运行新测试，必须得到真实的 `FAIL`，失败信息要指明具体行为（编译错误或 skip 不算）；然后恢复改动，确认通过。在提交说明或 PR 里写出它失败时打印了什么。
2. **跑全量，不只跑改过的包。** `wails3 task check` 与 CI 相同：gofmt、`go vet`、前端类型检查与 vitest、Go 全量测试（macOS 上带 `-race`）、Python 测试、需求追踪。守卫测试分布在你没动的包里（`internal/archtest` 的依赖规则、`TestEveryTestedPackageRunsInATestHome`），只跑自己的包会漏掉它们。
3. **启动 goroutine、定时器或改动锁的改动，推送前跑 `-race`**（`wails3 task test:race`）。
4. **master 上的红测试是现在就要修的 bug。** “在 master 上也一样失败”不是基线，“全量时失败、单独跑通过”也不是。判定为偶发之前，先 `go test -race -count=20 -run <名字> ./internal/<包>`，找到原因或开一个写明原因的 issue。
5. **交叉编译不等于在 Windows 上测过。** `GOOS=windows go vet/build` 不运行测试。Windows 相关的改动以 CI 的 `windows-latest` job 或真实 Windows 机器上的结果为准。
6. **日志完整保存到红测试被找到为止。** 链式命令用 `set -o pipefail`；把全量日志 `tee` 到文件再 grep，不要直接 `| tail`。

## 测试约定

- **每个有测试的包都在临时 HOME 里运行。** 包里有 `main_test.go`：`func TestMain(m *testing.M) { testenv.Main(m) }`。它把 HOME、USERPROFILE、XDG_*、APPDATA、LOCALAPPDATA 指到临时目录，并核对 `os.UserHomeDir/UserConfigDir/UserCacheDir` 都落在里面；新包漏写时 `internal/archtest` 会失败。测试不得读写真实的工作区、模型目录或用户配置。
- **需要插件环境就用 `testenv.Python(t)`**，不要自己拼 `.venv/bin/python`（Windows 上是 `Scripts\python.exe`），也不要自己 `t.Skip`。由插件解释器生成的夹具失败时调用 `testenv.Missing(t, …)`。缺少环境时默认跳过；设置 `TACET_REQUIRE_PLUGINS=1`（CI 与 `task test` 都会设置）则改为失败。插件环境由 `python/` 下的 `uv sync --locked --all-packages` 生成。
- **测试不联网。** 测试二进制里，`netgate` 的客户端只连本机（`NETWORK_TEST_OFFLINE`）。需要模拟供应商时用 `httptest` 起 TLS 服务，并把 `127.0.0.1` 放进允许列表。
- **会碰真实钥匙串的测试默认不跑。** 开头调用 `testenv.UseRealKeychain(t)`，设置 `TACET_REAL_KEYCHAIN=1` 才运行（CI 会设置）；本机运行前先征得机器主人同意。
- **重测试用环境变量开启**，不进默认套件：`TACET_SCALE`、`TACET_REAL_DOCX`、`TACET_EVAL_CORPUS`/`TACET_EVAL_REPORT`。依赖本地模型资源（`.pi/m0-resources`）的测试在资源缺失时跳过，CI 上不运行。
- **测试启动的东西在测试结束前收尾**：子进程、goroutine、定时器都要停止或等待结束，否则会写进下一个测试的目录。
- **CI 结果不能来自缓存**：CI 与 `task test` 都用 `-count=1`。

## 环境变量

| 变量 | 作用 |
| --- | --- |
| `TACET_REQUIRE_PLUGINS=1` | 缺少 `python/.venv`、插件解释器无法生成夹具时，测试失败而不是跳过 |
| `TACET_REAL_KEYCHAIN=1` | 运行使用真实登录钥匙串的 macOS 测试 |
| `TACET_TEST_HOME` | 由 `testenv` 设置；测试再次启动自身作为辅助进程时沿用父进程的临时 HOME |
