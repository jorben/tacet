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
- PR #5 第三轮审查（2026-11-03）：`TestAProbeStoppedBeforeItsWorkerStartsStartsNone` 断言关闭不超过 2 秒，这是经验值，不是需求规定的时限。改成和旧代码的行为对比：旧代码等整个探测，最多 3 秒，测试把探测卡到 Reap 之后，所以旧代码的关闭必然不少于 3 秒；关闭达到 3 秒才报错。
- PR #24 的审查（2026-11-04）：`TestAnInterruptEndsTheWaitOfARefusedStart` 改写时去掉了 20 秒保护。把 `discard` 里的 `Wait` 换成不响应中断的 context，旧测试只能等 `go test -timeout`（CI 上是 30 分钟）panic，同包后面的测试不再运行；恢复保护后，它在 20 秒报 `the run did not end on the interrupt`，后面的测试照常运行。这不算违反上面的规则：规则管的是给真实流程设的上限，这里被等的只有内存里的 fake，runner 变慢也不会误报。所以“出现”不加 1。
- 出现：2 次（2026-11-02、2026-11-03）

**一个修复由几处改动组成时，逐处撤回，确认每一处都有测试失败；哪一处撤回后都没有测试失败，就补一个会失败的测试，补不出来就删掉它。**
- PR #5 第二轮修复（8787011）给“Reap 之后不再启动探测 worker”写的第一个测试，钩子放在重读设置之前。撤回整个修复后测试照样通过，因为重读设置用的也是已取消的 ctx，在启动前就返回了。钩子挪到启动段前，再在段内加一个之后，分别撤回段内的停止检查和 Reap 前的等待，各有一个测试失败（`workers started`、`it started after the reap`）。
- 同一个提交里另有两处 ctx 检查（`probe` 开头、`modelArgs` 出错时）没有逐处撤回，第三轮审查指出它们冗余。实测时在 `modelArgs` 前加了模拟“正在校验文件”的钩子，分别撤回、一起撤回，所有探测测试都通过：循环在调用 `probe` 前刚检查过 ctx；探测从 `modelArgs` 只会拿到 `ErrModelNotInstalled`。两处都删掉了，代码里写明了原因。
- 同一轮审查还指出，`TestAProbeStartingItsWorkerWhenStoppedHasItReaped` 在 worker 根本没启动时也会通过。现在它断言 Reap 至少回收了 1 个进程；把 spawn 换成失败后，测试报 `the exit reclaimed 0 workers`。
- PR #12 第一版（cc0ef8c）的 `datadir_dev_test.go` 里有一条断言比较两个不同路径的 `credentialAccount`，在旧代码上也通过：它只重复了 `credential_test.go` 已经检查过的哈希性质，却让人以为测试证明了“钥匙串条目已分开”。把旧代码放回去时只看了整个测试失败没有，没有逐条看各个断言能不能失败。审查指出后删掉了这条断言。
- PR #37 第一版（3dcddaf）的提交说明写“27 处改动逐一撤回都有测试失败”，审查（2026-11-05）指出至少两处说不通。放回 3dcddaf 跑全量（529 个）复核：去掉 `live.ts:127` 重发前的 `inKept`、去掉 `BatchPage.tsx:13` 的批次核对，都没有测试失败。另外，标题写着“等在后面的单文件任务还在”的测试里并没有这个任务；删掉保留 `behind` 的代码后本文件 23 个测试全过，只靠别的文件才失败。这轮给这几处补了测试，并把修复里 16 处改动逐一撤回、各跑全量，每处都有测试失败（失败信息见 PR）。27 处里其余的没有重新逐处核对。
- 规则：
  - 逐处撤回，每撤回一处就跑一遍相关测试，记下哪个测试失败、打印了什么；
  - 写“逐处撤回都有测试失败”时，要能拿出每一处的撤回方式和失败信息，留不下记录就不写这个数字；同一行有几个条件（这里是 `inKept`、`await`）时，每个条件算一处；
  - 测试标题说了的事，要有对应的断言；
  - 钩子要放在被保护的那一小段旁边，放早了，前面别的检查会替修复挡住；
  - 撤回后没有测试失败的那一处，能写出会失败的测试就补上，写不出就删掉，并在代码里说明为什么不需要；同一个测试里，在旧代码上不会失败的断言也照此处理；
  - 断言“X 没发生”的测试，要另有断言证明前提成立（这里是 worker 真的启动过），否则前提不成立时它也会通过。
- 出现：2 次（2026-11-03、2026-11-05）

**测试用假进程替代真实进程时，假进程在被检查的属性上要和真实进程一致；“没有 X”的断言，要先做一次 X 确实存在的对照。**
- `probe_test.go` 的假 AstrLink worker 原来是一个 `#!/bin/sh` 脚本，在 `exec python` 之前以 `/bin/sh` 的身份等待。在 macOS 26.6.2 上，Apple 平台二进制的 `kern.procargs2` 不返回环境变量，退出流程按 `TACET_TREE` 标记回收时看不到这个进程。
  - `TestAProbeStartingItsWorkerWhenStoppedHasItReaped` 因此在本机必定失败（`the exit reclaimed 0 workers`），在 CI 的 macOS 15 上通过（#15）。
  - 更隐蔽的是“关闭后没有残留”的断言（`ReapMarkedPrefix(...) n != 0`）：去掉回收这一步后，`TestClosingDuringTheDeviceProbeEndsItsWorker` 在 sh 版本上照样通过，因为它根本看不到 sh 进程。
- 同一个假 worker 在 CI 的 macOS 15 上又造成另一种失败：PR #12 的 b11dcf4（运行 41592626908）里，`TestClosingDuringTheDeviceProbeEndsItsWorker` 报 `left running after the close: 1`。推测原因（本机 macOS 26 看不到 sh 进程，没有复现）：sh 的等待循环每 50ms 起一个 `/bin/sleep` 子进程，子进程也带着标记；回收只扫一遍进程列表，扫完之后、sh 被杀之前新起的 `sleep` 就漏掉了，随后的检查看到了它。真实 worker 在进程内等待，不会这样。
- 修复：fix/probe-test-platform-binary。假解释器改成用真实解释器运行的 Python 脚本（shebang 指向 venv 的 python），worker 从第一次 exec 起就带着可见的标记。验证：
  - sh 版本上测试失败，新版本通过；
  - 把 `probeStarted` 改成空函数，新版本报 `it started after the reap`；
  - 去掉回收这一步，新版本报 `left running after the close: 1`，sh 版本不报；
  - `-race -count=20` 全部通过。
- 规则：
  - 被检查的属性（这里是能否读到进程的环境变量、会不会派生子进程）随平台或二进制类型变化时，假进程要用和真实进程同类的二进制，行为也要一致，不能为了方便换成 shell；
  - 断言“没有残留”“没有发生”的测试，要另有一次 X 确实存在、断言确实报错的对照，否则检查手段失效时它也会通过；
  - 本机失败、CI 通过时，先比较两边的系统版本和二进制类型。
- 出现：1 次（2026-11-03）

**测试 setup 里替换全局函数的拦截，要放进一个单独、最先加载、不经 import 连到被拦对象的 setup 文件；并用测试证明拦截真的拦到了目标。**
- `frontend/src/test/setup.ts` 替换了 `window.setInterval`，打算在每个测试文件结束时清掉 `@wailsio/runtime` 的 `drag.js` 在导入时启动的轮询（每 50ms 一次，最长 5 秒）。但 setup.ts 自己的 import 链（`../dev/scenes` → `../actions/window` → `../services` → `./wails` → `@wailsio/runtime`）会在它的正文之前执行，runtime 启动轮询时替换还没装上，所以这个拦截从来没生效过。
  - PR #12 的 b11dcf4 在 CI `windows-latest` 上（运行 41592626908）：303 个测试全部通过，vitest 却报 `window is not defined`（`drag.js:69`，来自 `presetTest.test.ts`），以退出码 1 失败（#16）。
  - 这个 runner 慢，测试环境拆掉的时间正好落在定时器触发的间隙里，才会出错；其他运行都是绿的，本机也没有复现。
- 验证：在替换函数里按调用栈打印，旧位置没有打印，放进单独、最先加载的 setup 文件后打印了。
- 修复：fix/vitest-runtime-interval。拦截移到 `src/test/intervals.ts`，在 `setupFiles` 里排第一个。新测试断言 runtime 的轮询在“文件结束时会被清掉”的集合里；旧代码只补上测试要用的计数、不改变行为，测试报 `expected 0 to be greater than 0`。
- 规则：
  - 测试 setup 要在被测代码导入之前改掉全局对象时，放进单独的 setup 文件，排在最前，并确认它的 import 连不到被拦的模块（ES 模块的 import 会先于正文执行）；
  - 为防偶发问题加的拦截，要有一个测试证明它拦到了目标，不能只凭“之后没再出现”就认定有效。
- 出现：1 次（2026-11-03）

**行为随构建标签变化时，列出每一个构建入口，逐个核对它实际带不带这个标签。**
- PR #12 第一版（cc0ef8c）让工作区目录随 `production` 标签变化：开发构建用 `Tacet-dev`，正式构建仍用 `Tacet`。PR 描述和 README 都这么写，但审查（2026-11-03）指出有两处不成立：
  - `build:cli` 是不带标签的 `go build`，CLI 没有任何正式构建入口，所有 CLI 都会用 `Tacet-dev/cli-workspace`。改动前 CLI 的路径与标签无关，所以 README 表格里的 `Tacet/cli-workspace` 变成了一个没有任何构建会用到的路径。
  - README 把 `wails3 task run` 算作开发构建。实际上两个平台的 `run` 都不编译，只运行 `bin/` 里现有的可执行文件；如果上一次跑的是 `wails3 task build`（默认 production），它跑的就是正式构建。
- 第一版的测试在 `go test` 和 `go test -tags production` 下分别检查 `DefaultWorkspace`，只能证明“标签决定目录”，证明不了“每个入口带对了标签”。
- 修复：在同一个 PR 里修。`build:cli` 改成和桌面端一致，默认 `-tags production -trimpath`，`DEV=true` 时不带。用 `go version -m bin/cli/tacet` 核对：旧任务的产物没有 `-tags`；新任务默认显示 `-tags=production`，`DEV=true` 时没有。README 分别列出正式构建和开发构建的入口，并注明 `run` 不编译。
- 规则：
  - 行为随构建标签变化时，把 `Taskfile.yml`、`build/*/Taskfile.yml`、`build/config.yml` 里每个编译或运行二进制的入口都列出来，逐个写明带不带标签；只运行现成二进制的入口（`run`）要单独说明；
  - 用 `go version -m <二进制>` 核对产物实际的 `-tags`，不要从任务名推断。
- 出现：1 次（2026-11-03）

**把原因写进 PR 或提交之前，先用实验验证：按这个原因改变一个条件，结果要像预测的那样变化。**
- PR #12 第一版的描述说，`TestAProbeStartingItsWorkerWhenStoppedHasItReaped` 在 master 上也失败，所以不算这次改动的问题；这违反了 AGENTS.md 第 4 条，审查（2026-11-03）指出了这一点。描述里还写了原因：“`handshake` 抢先 `Terminate` 了 worker”。这个原因只是从日志推出来的，没有做过实验验证。
- 开 issue 之前补做了实验：在 spawn 之后、`handshake` 之前加 300ms 延迟，测试照样失败，推翻了这个解释。再往下查，真正的原因是：在 macOS 26.6.2 上，Apple 平台二进制（`/bin/sh`、`/bin/sleep`）的 `kern.procargs2` 不返回环境变量，假 worker 在等待期间是 `/bin/sh`，所以回收时按标记找不到它（#15）。
- 规则：
  - 写下的原因要附上验证它的实验：按这个原因改变一个条件（延迟、换进程、换平台），结果的变化和预测一致；没有做过实验，就写“推测”；
  - 本机失败、CI 通过时，先找两边环境的差别（系统版本、二进制类型），不要先找时序问题。
- 出现：1 次（2026-11-03）

**改变一个行为时，用旧行为的说法把需求、架构、模块设计和代码注释都搜一遍；需求文档最权威，不能只改它下游的文档。**
- PR #21（3f0cf9a）让模型映射改动不再询问、直接保存，同步改了架构和模块设计文档，但没有改需求文档的 FR-18（“缩小经确认的策略”）和 `narrowToSources` 的注释（“the screens ask before a change of rules or of a model's labels”）。模块设计文档写明需求以需求文档为准，于是权威文档反而和实现相反。需求追踪检查只比对摘要，文档内容对不对它查不出来。审查（2026-11-04）指出后，在 feat/model-page-copy-cleanup 里修复。
- 同一 PR 的修复（4c3b0f3）又写过了头：FR-18 写成“规则组改动”都会询问，实际只有 `editRuleset`（规则的启停、编辑、删除）会询问；回滚历史版本、在方案中移除规则组、切换检测方式或模型都直接生效。`docs/v1/backend/evidence.json` 也还写着“用户确认记录与 UI 未实现”。第二轮审查（2026-11-04）指出后，用临时测试逐条路径核实（既看类别是否失去来源，也看是否弹出询问，并以 `toggleRule` 作对照）再改写。
- 规则：
  - 写“X 改动会询问”这类范围结论之前，列出能造成同一后果的每个入口，逐个核对它是否真的走到那段代码，文档按入口写，不按“某类改动”概括；
  - 删除或改变一个行为时，用它的旧说法（这里是“确认”“询问”“confirmation”“ask”）搜索 `docs/v1`（含 `backend/evidence.json`）和相关代码的注释，逐处改掉或说明为什么保留；
  - `tools/backend_trace.py --check` 通过只说明清单是最新的，不说明文档内容和实现一致。
- 同一天 PR #22 的审查又查出代码注释里的旧说法：`detachCategory` 的调用处说界面会说明方案的变化（弹窗只逐项列模型），`CompatReport` 和 `Startable` 说 `NeedsNarrowing` 要用户确认（生产代码不读它）。修复在 fix/cli-resume-error-and-comments；`tacet-module-design.md:263` 的同类说法在 PR #24 的审查（2026-11-04）指出后，由 fix/pr24-followup-comments-test 改掉。
- 出现：1 次（2026-11-04）

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

**新增的后台 goroutine，要对每一种退出方式和每一个入口逐一核对：谁会启动它、谁让它停、关闭时谁等它；“关闭会等它”要有一个在它卡住时关闭的测试。**
- PR #5 第一版说“关闭时等待探测结束”，审查发现不成立：`waitProbe` 只挂在正常关闭的 Cancel 步骤上，`CloseForSystem` 不经过这一步，探测 worker 可能在 Reap 之后才起来；探测放在 `Open` 里，CLI 也会启动 worker；探测占着 `modelSem` 时，任务和试运行只能干等；关掉 GPU 后，排队中的探测仍会启动 worker。已有测试只覆盖“启动后握手成功”这一条路径。
- 修复：fix/model-device-probe。只有桌面应用传 `ProbeDevices: true`；探测有自己的 cancel，两种关闭都在 StopDispatch 时停止它；任务和试运行取槽前先停掉探测；关 GPU 时停掉探测。第二轮审查又指出，Reap 前等探测结束（最多 3 秒）会被不可取消的模型文件校验拖满，于是改成只等“检查是否已停止并启动进程”这一小段（`probeLaunch`）：已启动的 worker 由 Reap 回收，之后不会再启动。
- 证据：测试新增的字段让旧代码编译不过，编译错误不算失败，所以旧代码只补上测试要用的字段和钩子、不改行为，再跑新测试。第一版上：CLI 仍在探测、失败无原因码、任务和试运行一直等到测试超时、关 GPU 后探测不结束、无 provider 时无原因码；两个关闭测试也失败，但第一版不认 `ProbeDevices`，`Open` 自己起的探测和测试的钩子同时运行（报 DATA RACE），失败来自那个探测，只能算 CLI 问题的又一个表现，不能证明关闭的问题。第一轮修复后（f603dce）：关闭等探测 3 秒、无 provider 时无原因码。
- 怎样确认这个修复的每一处都有测试覆盖，见“## 验证”里的“逐处撤回”一条。
- 规则：
  - 给共享入口（`Open`）加后台工作时，列出所有调用方（桌面、CLI、测试），确认每个调用方都需要它；
  - 写“关闭时等 X”之前，对照 `lifecycle` 里每种关闭模式实际执行的步骤，而不是只看正常关闭；关闭只等真正需要等的那一段（这里是启动进程），不等可能卡住、又不可取消的工作；
  - 后台工作占用的共享资源（这里是 `modelSem`），要说明用户的真实操作来时谁让路。
- 出现：1 次（2026-11-03）

**依赖上一步成功的等待，先确认上一步成功了；已有的结论先输出，再做可能等待的收尾；判断“是不是被中断”要看 ctx，不能只看错误类型。**
- PR #22（18f6cee）让被拒绝的 `run` 在 `discard` 里依次 Cancel、Resume、Wait、Clean，审查（2026-11-04）指出：
  - `Resume` 的错误被忽略。它出错时 `run.Pending` 还在，`Wait` 只能靠 Ctrl-C 结束；拒绝信息又要等 `discard` 返回才输出，命令会无输出地挂起。测试的 fake 里 `Resume` 恒返回 nil，这条路径没有覆盖。
  - `Start` 的事务里 `getRun` 出错一律返回 `ErrNotFound`。在真实加密存储上，事务内取消后 `getRun` 的原始错误是 `context canceled`（20/20），到了 CLI 就成了 `NOT_FOUND`：本该可以 resume 的中断，被当作拒绝，草稿被取消并清理。完整的 `Start` 在 Linux 上开不了工作区（`WORKSPACE_LOCK_UNAVAILABLE`），这里只验证了 `getRun` 这一段。
  - `TestAnInterruptEndsTheWaitOfARefusedStart` 先取消 ctx，再让 fake 回答拒绝。读码看真实的 `Start` 不会这样回答（`pollIdle` 先返回 `Canceled`），测试把一个不会发生的组合固定成了预期。
- 审查建议的条件不能只改判断：`finish` 也只认 `context.Canceled`，把原错误传过去，命令仍然输出 `NOT_FOUND`、退出 1，还不暂停，所以改为传 `context.Canceled`。
- 修复：fix/cli-resume-error-and-comments。逐处撤回后的失败：`Resume` 出错仍等待报 `calls import,start,cancel,resume,wait,clean,close`；拒绝放回 `discard` 之后报 `while waiting ""`；去掉 ctx 判断、或把原错误传给 `finish`，都报 `code 1 err "tacet: NOT_FOUND\n"`；测试改回预先取消报 `code 3`。
- 规则：
  - 忽略一个调用的错误之前，看后面的步骤是否依赖它成功；依赖就按错误分支处理，并让 fake 能返回这个错误；
  - 收尾可能等待时，先把结论告诉用户；
  - 一个错误会被逐层改写时，要判断是否被中断就直接看 ctx，并核对接收方（这里是 `finish`）按什么判断；
  - fake 回答的组合要是真实实现能给出的，中断时机用钩子放在被测的那一步上。
- 出现：1 次（2026-11-04）

**发出去不等结果的写入，要在发出的那一刻登记；之后读同一份数据、依赖它的动作都排在它后面。同一份数据的写入也要互相排队。**
- PR #28 的 c85b683 让批次文件的“还有 N 处待你处理”离开再重开文件，审查（2026-11-04）实测：`leaveLiveFile` 把写入放进微任务，`openLiveFile` 同步发起读取，调用顺序是 `documentUnits,decide`。内核已存 `keep:true`，屏幕仍显示 `redact`；再确认一次，内核被改回 `keep:false`。反方向会把“涂抹”改回“保留”，原文被发布。已有测试没有改选择，也没有断言决定。
- 修的时候按同一类问题查了所有发 `decide` 的路径，又查出：
  - 两次写入同一份文件时，后发的先落地，再被先发的覆盖（旧代码：`expected [ false, true ] to deeply equal [ true, false ]`）；
  - 写入在路上时点导出、批量导出、重新处理：发布的是旧选择做出的候选，写入才落地；
  - 写入在路上时“保存并退出”直接退出，写入丢失；
  - 暂停后才落地的决定被内核静默丢弃（`EvDecisionsChange` 没有 `Paused`，`apply` 容忍），恢复后发布旧候选。真实内核复现：`the resume published what was built before the choice: "电话 13812345678\n"`；
  - `await syncSingle(await e.status(...))` 的回答晚到时，旧任务的文档被写到屏幕上的新任务上；批次文件开着时，批次事件按第一份文件判断，第一份已跳过就取消并清理整个批次（这一条在 6dc971c 上就有）。
- 同一轮审查还查出另外两类问题：读取失败被当成“没有文档”后销毁任务（`documentUnits` 报错 `() => false`，`showReview`、`restoreQuick` 的 catch 同样销毁）；单文件的授权在文件退回复核后还在，改选择会自动发布。`TestReviewDecisionsLetTheFileContinue` 把后者固定成了预期（快捷任务预先授权、决定后自动发布），但架构文档 6.1 节写的是该授权失效，而且这个组合没有生产路径（GUI 不授权启动，CLI 没有决定入口），所以改了这个测试。
- fake 的 `start` 立即返回并先报告 `preflighting`，真实内核的 `Start` 要等预检结束，第一次同步很可能就是等密码。fake 比真实实现多给了一步，`live.job` 为空的缺陷因此没暴露。
- 修复：所有写入经 `actions/decisions.ts`：发出时就登记（不放进微任务），同一文件串行，`decisionsSettled` / `allDecisionsSettled` 给读和动作等待；批次文件的入口留在当前文件；`Paused` 上的决定作用在它要回到的状态上；`syncSingle` 只认自己的批次。
- 规则：
  - 不等结果的写入（`void`、`queueMicrotask`）后面只要跟着读或动作，就把写入登记成可等待的对象，不能靠调用顺序；
  - 测试里让第一次写入卡住（`decideGate`），再做用户下一步会做的每件事；不卡住时 fake 立即完成，看不出顺序；
  - 异步调用的回答要先核对它是否还属于屏幕上的对象（批次、任务），再写进去；
  - `catch` 和 `() => false` 里做不可逆动作之前，先问这个错误是否真的说明对象不在了；
  - 容忍无效迁移（`quiet`）的调用，要列出被容忍的每个状态，看被忽略的事件有没有后果；
  - 修复一条路径时，把写同一份数据的其他路径都列出来逐个试。
- 同一条规则只做到了一半（#34、#35，2026-11-05）：
  - #34：这次修复让动作等写入“结束”，但结束不等于成功。`pending` 在写入结束时就删掉记录，`allDecisionsSettled` 只看在途的写入。写入失败、写入结束之后才点导出，发布的就是旧选择，内核里没有用户最后的决定；旧代码上新测试报 `expected [ 'decide', 'publish' ] to not include 'publish'`。
  - #35：`openLiveFile` 只核对了批次，没有核对这次打开是不是最后一次点击，`runId` 生成了却没参与判断。先点 A 再点 B、A 后读完时，屏幕跳回 A，B 上的选择没有写入内核；旧代码上新测试报 `expected 'a.txt' to be 'b.txt'`。
  - `路上的写入失败了：“保存并退出”不退出并说明，再点一次才退` 把“第二次点击能退出”固定成了预期。它能退出只是因为失败记录已经丢了，选择也跟着丢了。现在再点会重发，内核收下才退出，一直被拒绝时出路是“停止并清理”。
  - 第一版的新测试在旧代码上大多卡在提示文案（`expected 'STORE_FAILED' to contain '未能保存'`）或新字段（`expected undefined to be null`），看不出缺陷本身。前置步骤改成只等写入结束后，才报出上面的失败信息。
  - 修复：fix/issue-34-35-decision-writes。第一版（3dcddaf）写的“逐处撤回 27 处，每处都有测试失败”不成立，见“## 验证”里的“逐处撤回”一条。
- PR #37 第一轮审查（2026-11-05，同一天，“出现”不加 1）查出这个修复自己又违反了这条规则。两处都只在“等待期间人还在操作”时出现，而新测试都先备好写入、再开始等：
  - 3dcddaf 的 `saveOpenDecisions` 为了让重发排在屏幕上的选择前面，把屏幕上的选择推迟到 `await` 之后才发。等待期间在同一文件上改了选择并离开，新写入先到，旧快照后到，内核最后存的是旧选择。这是这个修复引入的回归，撤回后新测试报 `expected [ false, true ] to deeply equal [ true, false ]`。现在屏幕上的选择同步发出，写入清空后只重发不在屏幕上的文件。
  - `allDecisionsSettled` 只等调用那一刻已有的写入。W1 在路上时点导出，等待期间发出 W2，W1 一结束就发布，W2 失败也拦不住（`expected [ 'categories', …(12) ] to not include 'publish'`）；同样的窗口里，重发的旧选择还会排到 W2 后面盖住它（`expected [ true, false ] to deeply equal [ true ]`）。现在循环等到没有在途写入。
- 规则补充：
  - 等待写入之后，要分别回答“结束了吗”和“内核收下了吗”；失败记录留到下一次成功或对象不再需要为止，并列出每个“不再需要”的入口（这里是跳过、丢弃）；
  - 拦截动作的同时要测不会拦过头（别的批次、已跳过的文件、已丢弃的任务），这类测试在旧代码上本来就通过，要靠逐处撤回证明有效；
  - 撤回修复后看失败信息说的是不是缺陷本身，只卡在文案或新字段上不算证据；
  - “等写入”的测试要在等待开始之后再发一次写入，分别卡住两次（`decideGateOf`），不能只测等待开始前就在路上的写入；
  - 要让两次写入有先后时，不能把人最后的选择推迟到 `await` 之后：它先同步发出，旧的等清空后、仍没被覆盖才补发。
- 出现：2 次（2026-11-04、2026-11-05）

**状态机单测通过，不代表真实路径走得到；“等对方回报”的流程，要列出回报到不了的每一种情况，并从真实入口测到结束。**
- #33：处理中 `Cancel` 之后文件停在 `cancelling`，`Clean` 被拒，`Wait` 不结束，要再 Cancel 一次或重启。`Cancel` 等尝试回报，而 `Apply` 处理 `EvCancel` 时已清空 attempt 并把版本加 1，回报在 `AdmitTx`、`CompleteStage` 都被当成迟到结果丢弃，`transition.go` 里“由阶段确认取消”在真实路径上走不到。
- 没抓到的原因，每一层都只测了自己那一段：
  - `transition_test.go` 直接把 `Cancelling` 和结果喂给状态机，这条分支一直是绿的；`repository_test.go` 断言迟到结果被拒，也是对的，没有人把这两条放在一起走一遍；
  - `Active` 不统计 `cancelling`，`running()` 统计，所以看 `Active` 的测试看不出文件没停；
  - `FakeEngine` 的 `cancel` 立即变成 `cancelled`，`clean` 恒成功，前端测不出 `cancelLiveSingle` 吞掉的拒绝。fake 没实现被测的内核规则，和“状态与显示”里那一条同类；
  - PR #22 的 `discard` 注释写明了“With one running ... Clean below is refused”，把缺口写成了说明，没有问拒绝之后用户怎么办。
- 按“回报到不了”逐一列举，比 issue 多查出：
  - 尝试还在队列里，`disp.Cancel` 移除后根本没有回报（旧代码：`the file is cancelling`）；
  - 尝试已 `BeginStage` 但还没 `Submit`，调度器找不到它，文件取消后它才入队（旧代码：`a stage was left queued for the cancelled file`）；
  - 第二次 Cancel 看到 attempt 为空，在阶段还在跑时就宣布 `cancelled`（旧代码：`while its stage has not stopped the file is cancelled`）；暂停后取消同样如此，`Clean` 会删掉阶段正在用的数据；
  - `abandonSingle` 和 `cancelLiveSingle` 一样，`cancel` 后直接 `clean`，只有“停止并清理”会重试。
- 修复：fix/issue-33-cancel-stuck。`Cancel` 在一个事务里取消所有未结束的文件并清掉 `Pending`；`settleCancelled` 是结束 `cancelling` 的唯一入口，只在调度器已放手时结束文件；迟到结果到达时回到这里。`Running` 在提交内读，调度器在调用 completion 之前放手，所以两边谁先都不会漏。
- 验证：`internal/app` 的端到端测试带 `darwin` 标签，这次用不提交的 Linux 垫片跑；整体撤回后 14 个新测试失败，13 处逐处撤回各有测试失败。没有在 macOS、Windows 或真实界面上跑过。
- 规则：
  - 写“由 X 回报后结束”时，列出 X 回报不了的情况：还没开始、被移除、在交接途中、已被别的命令替换、被判为迟到；每种情况写明谁来收尾；
  - 新加一个终止路径的测试，要从用户命令（`Cancel`）走到终态，并接着做下一步（`Clean`、`Wait`、重启），不能只测状态机一步；
  - 把拒绝写进注释之前，先写明拒绝之后谁来解除；
  - 判断“忙不忙”的几处（`running()`、`Active`、`Terminal()`）含义不同时，断言要看用户下一步能不能做，不要只看其中一个计数。
- 出现：1 次（2026-11-05）

## 状态与显示

**操作中途失败留下的半成品，要测它经过关闭和重启，不能只测下一次操作。**
- PR #28（`90e49aa`）的 `TestAnImportThatStopsBeforeItsFilesLeavesNothingInTheWay` 只验证下一次导入能成功，没有关闭引擎。导入在两个事务之间停下时（`createKeys` 失败、进程在这里退出），`app_member` 的行已写入，`jobs` 的任务行还没有；`recoverJobs` 按成员行逐个恢复，`RecoverMember` 返回 `JOB_NOT_FOUND`，关闭失败，再打开走同一步也失败，工作区打不开。
- 发现：feat/parallel-same-kind 把这个测试加上关闭再打开，失败信息是 `closing with what the import left: JOB_NOT_FOUND`；在 `90e49aa` 上用同样的步骤复现，`Close` 和再次 `Open` 都返回 `JOB_NOT_FOUND`。
- 修复：`recoverJobs` 跳过任务行不存在的成员（`jobs.ErrJobMissing`）。
- 规则：
  - 为失败路径写测试时，留下半成品之后要关闭、重新打开，再断言；
  - 启动时遍历记录的步骤，要对照写入这些记录的事务边界，逐个想“只写了前一半”时它读到什么。
- 出现：1 次（2026-11-04）

**界面上的结论如果来自内存里的记录，要测重启之后显示什么。**
- 推理设备由插件握手回报，记在 `Engine.modelBackends` 里，这份记录只在内存中。下载后的试运行确认过设备，所以下载完那一刻显示正确；重启后 `Recover` 把模型改回 installed，界面仍是"已就绪"，内存记录却已清空，模型卡退回"GPU 优先 · 启动时检测"，直到第一次跑任务才有结论。已有的测试都在同一个引擎里下载后立即检查，没有一个关掉再重开，所以没抓到。这个问题由 Jorben 在真机上发现（2026-11-02）。
- 修复：fix/model-device-probe。应用启动时，对已安装的 GPU 兼容模型在后台起 worker，握手后立即结束，不试运行；探测期间返回 `Detecting`。新测试 `TestTheDeviceOfAnInstalledModelIsConfirmedAtStartUp` 用假 worker 回报 Core ML，不依赖模型资源。旧代码上失败：`before the handshake: device "auto" detecting false, want auto and detecting`。
- 规则：一个显示值只要依赖内存里的状态，测试就要把引擎关掉、用同一个工作区重新打开，再检查显示；不能只测同一个会话里的流程。
- 出现：1 次（2026-11-02）

**缺省值随某个状态变化时，状态切换的那一刻要把旧缺省值落成显式值；测试要走完“切换前 → 切换 → 切换后再编辑”。**
- PR #19 的 8309009 让方案类别的缺省处理随 `stored` 变化：没存过的方案按遮盖算，存过的按关闭算（`presetTreatment`）。新建方案的 `cats` 是空的，第一次写入按缺省遮盖存下了有来源的类别，但写入成功后的 `store.update` 只回填了 `stored`，`cats` 仍为空。从这一刻起界面、`unhandledCategories`、试运行都把这些类别当作关闭；之后改任何一个类别，重新生成的内容里就没有它们了，存储里的遮盖被删掉。
- 第一轮审查的修复（3132e2b）只改了报告里的那一个案例（未存方案不给无来源类别写缺省遮盖），测试停在第一次写入，没有在写入之后再编辑一次，所以没抓到。同一个混淆还在 `SchemeEditor` 的提示里：它用带缺省值的 `presetTreatment` 判断“已保存的设置”，未存方案的无来源类别会被说成有保存的设置，实际上什么也不会存。
- 修复（第二轮审查，2026-11-03）：第一次写入成功时，把写入的方案对象的设置补进 `cats`（`schemeTreatments`，与读取存储用同一个转换）；提示只看方案自己持有的 `cats`。新测试 `新建方案存好后仍遮盖有来源的类别；之后改别的类别不会删掉它们` 在旧代码上报 `expected 'off' to be 'mask'`；去掉前面的断言后，报存储里 phone 为 `undefined`。
- 规则：
  - 一个推导值的缺省取决于某个标志（这里是 `stored`）时，列出设置这个标志的每一处，在那里把旧缺省落成显式值，标志变化前后结果要一样；
  - 界面上说“已保存”“会保留”的地方，只看显式持有的值，不看带缺省的推导值；
  - 测试不能停在切换那一刻，要在切换之后再做一次编辑，检查存储里原有的值还在。
- 出现：1 次（2026-11-03）

**界面说“已保存、可继续”，或要清空、覆盖屏幕上的任务之前，先确认内核真的这样做了；前端测试的 fake 要实现被测的那条内核规则。**
- a56e3ab（2026-10-31）接入真实引擎时，界面和内核对“名额”各有一套判断。2026-11-04 排查“待确认时开不了新任务”时查出三处，都已合并数日，CI 全绿（报告 tacet-concurrency-analysis.md 第 2.4 节，这次写测试确认）：
  - `startLiveBatch` 先覆盖 `d.batch`，再调用引擎；引擎返回 `BATCH_ACTIVE` 后把它置空。只剩待处理文件的批次因此从界面消失，直到重启。
  - `stopLiveBatch(true)` 提示“已暂停，可在‘最近’中继续”并清空屏幕，暂停却不是终态，内核仍占着名额，下一批还是 `BATCH_ACTIVE`。
  - 5bd694f 起，CLI 的待复核批次占着名额，`clean` 也拒绝，没有任何命令能解除，默认要等 7 天保留期。`TestCLIReviewNeededExitsTwoAndStaysIndependent` 把 `clean` 的拒绝固定成了预期，没有人问过拒绝之后用户怎么办。
- 前端测试没抓到，是因为 `FakeEngine` 没有名额这个概念：`importFiles` 只会按 `importFails` 失败，而测试里的 `importFails` 都设在屏幕为空的时候，从没覆盖“屏幕上有任务、引擎拒绝”。
- 修复：feat/park-pending-tasks。屏幕改为在内核接受导入后才变；`FakeEngine` 按内核规则判断名额（草稿不让路、只剩等人的文件才让路）。在旧实现上，新测试和 CLI 端到端测试都真实失败，失败信息见 PR。
- 同一 PR 里两个相近的错误：
  - `park_test.go` 的第一版把 `put` 的 `confirmed` 参数传反了，`TestParkingLooksAtEveryMember` 和 `TestAParkedBatchFinishesLikeAnyOther` 因为“草稿本来就不停放”而空跑通过。现在都先断言确实停放了（对照）。
  - `createSingle` 的 `out` 缺省值是演示用的“桌面 / 已脱敏”。重启后列出的单文件若不显式给 `out: ""`，导出时会带着这个相对路径去找引擎。写“选择继续后的输出位置”这条断言时才发现。
- 同一 PR 的审查（2026-11-04）查出这条规则的主场景还是没做到，测试都绕开了用户走的那条路：
  - 停放批次的状态文案以“已导出 0 / 2 · …”开头，`openHistoryEntry` 看到前缀就开结果页，到不了继续弹窗。parking 测试都直接调 `resumeSavedBatch`，没有一个从列表点进去；
  - 结果页的“去处理”和批次文件里的“还有 N 处待你处理”都进了 `enterBatchReview` 的演示复核，决定到不了内核。审查报了前一个入口，后一个是修复时按“列出每个入口”查出来的，所以守卫放在 `enterBatchReview` 里，不放在调用方；
  - `failed` 在屏幕上算“已结算”，内核却没有结束它：批次当结果收起，没有重试入口，退出清理时也被漏掉。修在 `batchStatus` 一处，收起、关闭提示和退出清理都读它；
  - 单文件的选择只在导出时才发给内核，“已保存到‘最近’”重启后就没了。上一版测试还写着 `expect(fake.decisions).toBeNull()`，把缺口固定成了预期；
  - 重启后继续一个导出时受阻的单文件，`syncSingle` 按“处理前受阻”取消并清理。修的时候又查出两层：“可重试导出”的提示从来不成立（`exportLiveSingle` 不调 `retry`，`Publish` 只推进 `ready_to_publish`）；只补 `retry` 还会出隐私问题（`blocked` 上的决定变更是无效迁移，重试直接发布按旧决定做出的候选）。真实内核上复现：先保留、受阻、再改成涂抹，旧代码重试后发布的是 `"电话 13812345678\n"`。
- 验证：`internal/app` 的端到端测试带 `darwin` 标签，Linux 上开不了工作区。这次在临时目录里给 `platform` 补了不提交的 Linux 垫片（flock、renameat2、文件密钥），去掉标签后 12 个停放测试和整个 `internal/app` 都能跑（只有依赖进程回收的 2 个测试因垫片失败）。修复前后都用真实内核跑过，状态机测试和 fake 给不出上面那个隐私问题，也给不出“重启后是 `paused`，继续之后才是 `blocked`”。
- 同一天的 fix/model-redownload-repair 是同一条规则，所以“出现”不加 1。文件校验未通过时，界面只给“重新下载”，实际执行 `Discard` 再 `Download`。5bd694f 起，已安装目录还在时 `Download` 返回 `MODEL_NOT_DOWNLOADABLE`，`TestAFailedRedownloadNeverReplacesAnInstalledModel` 把这个拒绝固定成预期（注释写着 download is not the repair），没人问拒绝之后用户怎么办：
  - `DownloadModel` 是 `go e.catalog.Download(...)`，拒绝不会到界面；`Discard` 已把状态改成 `installed`，文件其实是坏的，而前端把 `installed` 映射成“已就绪”（这一步是读代码得出的，没有在界面上跑过）。目录里多一个 `.DS_Store`，就只能手动删文件才能恢复。
  - 前端的 `FakeEngine.discardModel` 恒回到 `pending`，测试碰不到这条路。
  - 只去掉拒绝还不够：加载失败后点“重新下载”，副本完好也会再下一遍，下完被 `finish` 扔掉；断网时“加载失败”还会变成“网络错误”。
  - 修复：`Discard` 不改错误态；`Download` 在副本仍匹配时不联网，直接重新加载；不匹配时下载，通过校验才替换。旧 `download.go` 上 5 个测试都失败（`the download again did not run`、`after the discard, files that do not match are shown as`、`download again: MODEL_NOT_DOWNLOADABLE` 等），4 个变异逐个放回都有测试失败。只在服务层验证：`internal/app` 里模型相关的端到端测试依赖本地模型资源，本机和 CI 上都不运行。
- #36（2026-11-05）是上面 fix/model-redownload-repair 之后的修了又修，也是同一条规则：`DownloadModel` 只起一个 goroutine，`Download` 又在写任何状态之前先做全量哈希（真实模型要几秒）。前端写入 `downloading` 后马上读回 `error`，判定不忙就停止轮询，内核在后台下载到就绪，屏幕一直显示“校验未通过”。`FakeEngine.downloadModel` 在返回前就改了状态，`discardModel` 恒回到 `pending`，所以测试从没读到过那个旧状态。
  - 修复：`Download` 哈希前写 `verifying` 并保留错误码，传输被拒绝时恢复，重启后由 `Recover` 恢复；预检改为全量哈希（`MODEL_PREPARE_FAILED` 后多出文件时第一次点击就修复），通过后直接加载，不再哈希第二遍；前端在内核接手前保持屏幕状态，最多 5 秒；fake 的状态改为晚于调用返回，`discard` 按内核规则走。
  - fake 改完后又查出一层：没有已安装副本时，`Discard` 把校验失败的模型改成 `pending`，“读到的状态变了”不能算内核已接手。
  - 只在服务层和 fake 上验证：`internal/app` 的端到端测试带 `darwin` 标签，这里没跑，也没有在真机上点过。
- 规则：
  - 接口只是起后台工作时，返回不代表状态已变：fake 的状态要晚于返回才变，测试让内核迟迟不接手，再读一次屏幕；
  - 动作会让用户相信某件事（“可继续”“已清理”），测试就断言内核状态，并接着做用户下一步会做的事（再开一批、再重启一次）；
  - 测试从用户点的那个入口进去（列表条目、页面按钮），不要直接调入口背后的函数；
  - 屏幕上的汇总状态（“已结算”“已结束”）要和内核的终态逐个对照，不一致的状态（这里是 `failed`）单独写测试；
  - 提示里说“可重试”“可继续”，就走完重试本身，并断言结果里是用户最后的选择；
  - 清空或覆盖屏幕上的状态放在内核接受之后，拒绝不能改变屏幕；
  - fake 要能给出真实实现的拒绝，并让测试在屏幕上有任务时触发；
  - 把“拒绝”固定成预期的测试，要写明拒绝之后用户还能做什么；答不出来就是缺口。
- 出现：2 次（2026-11-04、2026-11-05）

**拦住一个动作时，写明出路的代价；代价超过受影响的范围，就是缺口。给内核加一个新操作，要用真实内核接着走完用户下一步。**
- PR #37 让写入失败的选择拦住导出和“保存并退出”，出路只有“停止并清理”，而它清掉全部未完成任务，包括“最近”里的。一个文件写不进去，代价是所有任务的进度。提示如实写了这个代价，测试也把这句文案固定成了预期，但没有人问这个代价对不对。审查（2026-11-05 低级第 5 条）指出后开了 #41。
- 修复：fix/41-stop-clean-scoped。有写入失败时只清理这些文件（新增 `CancelFile`、`CleanFile`）或单文件任务，其他任务按“保存并关闭”保留。
- 按文件取消第一次写好后，真实内核的端到端测试接着走下一步才报错：开始前被取消一个文件后 `Start` 返回 `JOB_TRANSITION_INVALID`，`ConfirmBatch` 只放过 `unprocessable`。批次级 `Cancel` 从来碰不到这一点，因为它取消所有文件，批次随之结束；FakeEngine 没有这条规则，也碰不到。现在的界面走不到这条路（选择只在开始之后才写），但 `CancelFile` 说的是“其他文件继续”，所以修在内核：被取消的文件不进入这次开始（`TestConfirmingABatchLeavesOutAFileCancelledBeforeIt`）。
- 这些端到端测试带 `darwin` 标签，第一次在 Linux 垫片上跑时因为没有 `python/.venv` 全部跳过，输出是 `ok 0.019s`。耗时不对才发现，见“CI 绿了不代表测试跑过”那一条。
- 规则：
  - 新增拦截时写明出路的代价，和受影响的范围比一比；出路更贵时，当场缩小它或开 issue，不要只把代价写进提示；
  - 把原来只有整批的操作拆成单个对象时，列出假设“全部一起变”的代码（这里是 `ConfirmBatch`、`Start` 的格式统计），并用真实内核接着做下一步（开始、重启、导出）；
  - 范围取决于会变化的状态（这里是写入失败）时，对话框要跟着状态变；点下按钮后范围变了，只能往破坏更小的方向变。
- 出现：1 次（2026-11-06）

## 校验与缓存

**按（大小、修改时间）跳过校验的缓存，测试要把修改时间设回原值，不能只把它推后；缓存的零值不能和出错时的返回值相等。**
- 加载模型前的文件核对（`internal/models` 的 `checkFiles`）按文件名、大小、修改时间算指纹，和上次全量哈希时的指纹相同就不再哈希。实测的 Linux 虚拟机（HZ=1000）上文件时间的精度是 1ms，tmpfs 上写得够快，下载完的文件和随后同样大小的改写落在同一毫秒，修改时间不变，被改过的文件通过了 `Prepare` 并被加载（#29）。
  - `TestPrepareRefusesWhatIsNotInstalledAndFilesThatChanged` 在 Linux 上偶发失败，失败日志没留下。按 issue 的建议 `-race -count=200` 重跑，macOS 和 Linux overlay 都复现不了：两次写之间最少隔 1.4ms（macOS，时间精度不到 1ms）和 4ms（overlay）。把临时目录放进 tmpfs、不带 `-race` 之后，2000 次里失败 1142 次（`models_test.go:523: <nil>`）；300 次里有 193 次两次写的修改时间完全相同。只关掉缓存，2000 次零失败。
  - 已有的 `TestACachedVerificationIsRedoneWhenAFileChangesWithoutChangingSize` 把修改时间推后一小时，测的是“修改时间变了会重新哈希”，没有覆盖“修改时间没变”。CI 的 Go job 只跑 macOS 和 Windows，没有 Linux。issue 报告的 `-race` 环境没有复现，原始日志已丢失，所以那一次失败是否同因只是推断。
- 同一处还有一个缓存问题，复现时一起发现：所列文件换成符号链接时 `stamp` 返回 `"", nil`，重启后缓存为空，也是 `""`，两者相等，校验被跳过，指向包外的链接被当成模型文件。
- 修复：fix/issue-29-model-verify。`Prepare` 加载前每次全量哈希；`CheckBinding` 仍用缓存，所列文件不是普通文件时报错而不是给出指纹。测试用 `os.Chtimes` 把修改时间设回改写前的值，不依赖时钟。旧代码上 4 个测试都失败（涉及 `Prepare` 的 3 个报 `<nil>`，符号链接的报 `a link was taken for the model file`）；6 个变异（含“`CheckBinding` 也全量哈希”）逐个放回，各有测试失败；tmpfs 上 2000 次、整包 30 次、`-race` 200 次全部通过。
- 规则：
  - 用便宜的指纹代替全量校验时，写明它漏掉什么（这里是同大小、同一时钟刻度内的改写），结论要求“每次都校验”的地方（加载前，架构 8.3 节）不用这个缓存；
  - 测缓存失效，既要测指纹变了，也要测内容变了而指纹不变，后者用 `os.Chtimes` 设回原值，确定性地造出来；
  - 出错时返回的值（这里是 `""`）不能等于缓存的零值，否则空缓存会“命中”一个出错的结果；
  - 偶发失败重跑复现不了时，按怀疑的原因改变一个条件（这里是换成 tmpfs、关掉 `-race`），不要只加大 `-count`。
- 出现：1 次（2026-11-04）

## 界面布局

**改了一行的布局方式（换行、收缩、溢出），要在最小窗口宽度下、用最长的内容，在真实浏览器里量；审查建议的 CSS 也要量过再用，每条规则撤回后都要看到变化。**
- PR #25（2f0a744）把 `.mapping-labels` 的 `flex-wrap:wrap` 改成不换行，而 `.cat-row>.rv` 是 `flex:none`，标签区既不换行也不收缩。测试只跑 jsdom，没有布局，抓不到。审查（2026-11-04）指出后，在 Chromium 里实测：自定义类别带 3 个现有标签和 `+2` 时，窗口 960px 重叠 51px，1011px 刚好不重叠；3 个长标签时面板横向溢出 363px（窗口 1000px），下拉箭头和删除按钮被挤到面板外。窗口最小宽度是 960px（`cmd/tacet-desktop/main.go`），默认数据每个类别只有 1 个标签，看不出问题。
- 按审查建议只放开 `.rv` 的收缩，控件回来了，但重叠还在（长标签 960px 时 38px，现有标签 960px 时 6px）：`.cat-body` 也是 `min-width:0` 且可收缩，会按比例缩到内容以下，内容溢出到标签区。加上 `.mapping-row>.cat-body{flex:1 0 auto}` 才归零。
- 逐条撤回 CSS 规则后，`.label-chip{min-width:0}` 和 `+N` 的 `flex:none` 没有任何变化（`overflow:hidden` 的 flex 项自动最小宽度就是 0；`min-width:auto` 已保证 `+N` 不小于内容），两条都删掉了。
- 修复：fix/mapping-labels-narrow-layout。非 CSS 部分逐处撤回后都有测试失败：去掉 `title` 报 `expected [ '', '', '' ] to deeply equal`，去掉 `wrap` 报 `data-tw`，`+N` 不响应点击报 `expected '' to be ...`。
- 规则：
  - 布局改动要造出最坏的状态（最小窗口、最长内容、控件最多的那一行）再量，默认数据不算；
  - 量的是“名称右缘和标签左缘的距离、面板 `scrollWidth`、控件是否还在面板内”这类数字，不只看截图；
  - flex 行里要指定谁让路：只放开一边时，另一边若也是 `min-width:0`，会一起缩；
  - jsdom 量不了布局，这类规则没有自动化测试时要写明，并附上实测数字。
- 出现：1 次（2026-11-04）

## 原生窗口

**网页画的界面要和系统控件对齐时，先在真机截图里量出两边的位置，再定尺寸；不按预设名或文档推断。**
- 7b60ffe（2026-10-30）接入桌面外壳：窗口用 Wails 的 `MacTitleBarHiddenInset`，网页沿用原型 44px 的标题栏，左边留 84px 给红绿灯。真机上红绿灯明显偏下（2026-11-02 Jorben 报告），CI 没有任何检查覆盖这件事。
- 量出来的原因有两层：
  - `HiddenInset` 带一个工具栏，没设样式就按 unified 处理：AppKit 把红绿灯中心放在距顶 26pt，而 44px 标题栏的中线在 22pt；
  - Wails v3.0.0 的内容视图是 宽-1 × 高-1，WKWebView 是 宽 × 高，原点都在左下角，所以网页顶上 1px 落在窗口外面，页面 y = 窗口 y + 1。只把标题栏改成 40px 时，截图里边框在 38pt，中线比红绿灯高 1pt。
- 测量方法：用 Swift 建一个同样配置的 NSWindow，读 `standardWindowButton` 的 frame，各工具栏样式的红绿灯中心依次是 automatic/unified 26、unified compact 20、expanded 16；再用 `screencapture -l` 截构建出来的应用，按像素量红绿灯和标题栏边框；用一个原生对照窗口确认截图本身没有裁剪。
- 修复：fix/titlebar-traffic-lights。工具栏改为 unified compact；macOS 下标题栏为 42px（1px 在窗口外，40px 可见，1px 边框），顶部内边距 1px。`cmd/tacet-desktop/titlebar_test.go` 核对红绿灯中心与可见标题栏、标题栏内容的中线一致，Wails 版本变了就失败，提醒重新测量。
- 规则：原生控件的位置取决于系统版本、窗口样式和框架怎么摆放 WebView，三者都要在截图里核实；改对齐时给测试写下实测值和测量方法。
- 出现：1 次（2026-11-02）
