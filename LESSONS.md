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

**测试不用墙钟时间给整个流程设上限；改变运行方式时，重新检查依赖时长的上限。**
- PR #3 加上 `-race` 之后，只改了文档的 e8a22d8 在 CI 上变红了（运行 41391573277），而同样代码的上一轮 bded2e0 是绿的：
  - macOS：`TestASystemShutdownStopsAtOnceAndTheBatchResumesAfterwards` 用一个 4 分钟的 context 覆盖导入、处理、关机、恢复、再处理的全过程。本机 `-race` 下要 122s，CI 上跑了 241s，超过了上限。
  - Windows：`internal/jobs` 的 `fixtureDB` 用一个 10 秒的 context 覆盖整个测试的所有提交。那次 runner 整体偏慢，`internal/models` 从 26–32s 变成 80s，`internal/policy` 从 8–10s 变成 50s，结果 3 个测试报 `context deadline exceeded`。
- 复现方法是注入延迟，不是反复重跑：
  - 关机测试：旧代码的上限压到 6s，在同一行（`engine_e2e_test.go:1957`）复现了同样的错误；
  - `jobs`：上限设为 100ms，每次提交前 sleep 5ms，在 `checkpoint_test.go:194` 和 `invalidate_test.go:70` 复现了 CI 上的报错。
  - 改成 `t.Context()` 以后，两者都通过：关机测试在本机 `-race` 下用 122s，`jobs` 在注入同样的延迟后通过。
- 规则：
  - 防卡死交给 `go test -timeout`，测试只给需求本身规定的时限设断言，例如“关机在 4 秒内完成”；
  - 加 `-race`、上新平台或换 runner 时，先找出离上限最近的测试，在本机 `-race` 下看各测试的耗时。
  - 其他测试里还有约 80 处 `context.WithTimeout(context.Background(), …)`；在本机 `-race` 下，它们离各自的上限都还很远，所以这次没有改。
- 出现：1 次（2026-11-02）

## CI 耗时

**优化 CI 耗时之前，先拿到 job 里每一段的实测时间，再给出预计；不要只按最慢的那一段推算。**
- PR #3 的第一轮优化（a40d925）把 `internal/app` 拆成 3 个进程并行，把前端步骤只放在 Windows 上跑。动手前预计 macOS go job 从约 10 分钟降到 5–6 分钟，实测只从约 604s 降到 497s（运行 41390041951 → 41399127060）。
- 预计偏差的原因：只估了分片那一段，漏了它前面串行的两段。按日志时间戳，测试步骤实际是三段依次执行：其他 40 个包带 `-race` 约 110s（大部分是编译），编译 `internal/app` 的测试二进制约 37s，3 个分片 288s。
- 规则：
  - 先用 `gh api repos/<owner>/<repo>/actions/runs/<id>/jobs` 拿每个步骤的耗时，再用步骤日志的时间戳把耗时最长的步骤拆成串行的几段；
  - 给出的收益写成“推算”，改完以实际 CI 耗时为准，和推算不符就照实说。
- 出现：1 次（2026-11-02）

**`-race` 慢，先做 CPU 剖析看时间花在谁身上，再决定怎么拆分或并行。**
- 第一轮优化后，3 个分片要等关机测试跑完，它在本机 `-race` 下要 125s，不带 `-race` 只要 16s。在 `-race` 下用 `-cpuprofile` 剖析：约三分之一的样本落在 `go-sqlite3-wasm`（`_sqlite3VdbeExec` 累计 37s / 114s）。这个依赖是从 WebAssembly 转译成 Go 的 SQLite 解释器，race detector 给它的每次内存读写都插了桩。
- 58bcf97 用 `-gcflags=github.com/ncruces/go-sqlite3-wasm/v6=-race=false` 让这个包不插桩：关机测试 125s → 43s，`internal/app` 3 个分片 143s → 61s，其他包 38s，全部通过。
- 去掉插桩之前，先验证 race 覆盖还在：在 `internal/app` 里临时放一个故意制造的竞争，用同样的参数跑，仍然报 `WARNING: DATA RACE` 和 FAIL，验证完删掉。代价是第三方 SQLite 内部的竞争不再检查；tacet 自己的代码、驱动和 `database/sql` 仍然插桩。
- 规则：
  - 拆分、并行只能把串行时间摊开，先剖析，找到时间是不是集中在某个依赖上；
  - 只对第三方、且不经 tacet 代码共享状态的包关闭插桩，并用一个故意制造的竞争证明 tacet 的代码仍被检查；
  - CI 和 `Taskfile.yml` 的 `test:race` 用同一组参数，本机和 CI 的结论才能对得上。
- 出现：1 次（2026-11-02）

**改了构建参数（`-race`、`-tags`、`-gcflags`），要确认 CI 的编译缓存会被重新保存。**
- `actions/setup-go` 的缓存 key 原来只按 `go.sum` 计算。这个 key 在 CI 加 `-race` 之前就保存过；之后每次都命中主 key，而命中主 key 时不会再保存（日志：`Cache hit occurred on the primary key …, not saving cache`），所以 `-race` 的编译产物从来没进缓存，每次都从头编。
- 58bcf97 把 `ci.yml` 加进 `cache-dependency-path`。新 key 第一次运行缓存未命中，macOS go job 429s（`go vet` 77s、测试 294s）；同一提交重跑、缓存命中后是 230s（测试 192s）（运行 41411160316 及其重跑）。缓存这一项就省了约 200s，比本机冷热编译的差距（`internal/app` 的 race 二进制 12s 对 2s）大得多。
- 规则：
  - 改了编译参数，看 `Post Run actions/setup-go` 的日志：写着 `not saving` 而参数又变了，说明缓存里没有新参数的产物；
  - 缓存 key 要覆盖决定编译产物的文件（参数写在 `ci.yml` 里，就把它放进 key）；
  - 评估优化效果时分开报缓存命中和未命中两种耗时：`ci.yml` 每改一次，以及合并后 master 的第一次运行，都是冷缓存（PR 分支保存的缓存，master 用不到）。
- 出现：1 次（2026-11-02）

## 并发与时序

**等待“某一步做完”的判断，要列出被等的对象在途中可能处在的每个状态，包括排队等调度的中间状态；用钩子推迟调度来测，不靠反复重跑。**
- PR #3 合并后，master 的 macOS go job 变红（运行 41419681662）：`TestAnInterruptedStartIsCarriedOutByResume` 卡满测试自己的 2 分钟轮询上限（120.53s）后失败，最终状态是 `Started:false Authorized:false`。同一份代码在 PR 上的 5 轮 CI 都是绿的，本机 `-race -count=20` 也全部通过（每次约 1.1s）。
- 原因：暂停一个正在检查的文件再恢复，它先回到 `queued`（cursor=preflight），要等调度器 `BeginStage` 才重新变成 `preflighting`。`pollIdle(preflightOnly)` 只把 `preflighting` 当作检查没完，Resume 带起来的 Start 落在这个窗口里去 `ConfirmBatch`，得到 `JOB_TRANSITION_INVALID`；`finishPendingStart` 随即清掉请求，用户给的开始和授权被静默丢弃。CI 上 3 个分片占满 3 个核，调度慢了一点就落进了窗口。同一个窗口还会让“恢复后马上开始”报错，让 `Preflight()` 在文件还在排队时就返回。
- 复现：在 `Engine.dispatch` 开头注入 300ms 延迟，同一行（`engine_e2e_test.go:1497`）复现了同样的状态，并打印出后台 Start 的错误 `JOB_TRANSITION_INVALID`。第一次把延迟加在检查阶段里面，复现不了：窗口在恢复之后、调度之前，不在检查过程中。
- 修复：fix/resume-carries-start。`pollIdle` 把 cursor 是 preflight 的 `queued` 也算作检查没完；新增测试钩子 `beforeDispatch`，3 个测试把检查的派发推迟 500ms，在旧代码上都失败（`the start Resume carries out was lost`、`JOB_TRANSITION_INVALID`、`State:queued`），修复后 `-race -count=20` 全部通过。
- 规则：
  - 写“等到 X 做完”的判断时，从状态机里找出 X 在途中会经过的所有状态（这里漏了 cursor 指向该阶段的 `queued`），不要只认“正在做”的那一个；
  - 涉及异步调度的测试，用钩子推迟调度，确定性地打开窗口；本机反复重跑都通过，不能说明没有竞争。
- 出现：1 次（2026-11-02）

## 状态与显示

**界面上的结论如果来自内存里的记录，要测重启之后显示什么。**
- 推理设备由插件握手回报，记在 `Engine.modelBackends` 里，这份记录只在内存中。下载后的试运行确认过设备，所以下载完那一刻显示正确；重启后 `Recover` 把模型改回 installed，界面仍是"已就绪"，内存记录却已清空，模型卡退回"GPU 优先 · 启动时检测"，直到第一次跑任务才有结论。已有的测试都在同一个引擎里下载后立即检查，没有一个关掉再重开，所以没抓到。这个问题由 Jorben 在真机上发现（2026-11-02）。
- 修复：fix/model-device-probe。应用启动时，对已安装的 GPU 兼容模型在后台起 worker，握手后立即结束，不试运行；探测期间返回 `Detecting`。新测试 `TestTheDeviceOfAnInstalledModelIsConfirmedAtStartUp` 用假 worker 回报 Core ML，不依赖模型资源。旧代码上失败：`before the handshake: device "auto" detecting false, want auto and detecting`。
- 规则：一个显示值只要依赖内存里的状态，测试就要把引擎关掉、用同一个工作区重新打开，再检查显示；不能只测同一个会话里的流程。
- 出现：1 次（2026-11-02）
