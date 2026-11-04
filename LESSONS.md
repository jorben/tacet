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
- 规则：
  - 逐处撤回，每撤回一处就跑一遍相关测试，记下哪个测试失败、打印了什么；
  - 钩子要放在被保护的那一小段旁边，放早了，前面别的检查会替修复挡住；
  - 撤回后没有测试失败的那一处，能写出会失败的测试就补上，写不出就删掉，并在代码里说明为什么不需要；同一个测试里，在旧代码上不会失败的断言也照此处理；
  - 断言“X 没发生”的测试，要另有断言证明前提成立（这里是 worker 真的启动过），否则前提不成立时它也会通过。
- 出现：1 次（2026-11-03）

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

## 状态与显示

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
- 规则：
  - 动作会让用户相信某件事（“可继续”“已清理”），测试就断言内核状态，并接着做用户下一步会做的事（再开一批、再重启一次）；
  - 测试从用户点的那个入口进去（列表条目、页面按钮），不要直接调入口背后的函数；
  - 屏幕上的汇总状态（“已结算”“已结束”）要和内核的终态逐个对照，不一致的状态（这里是 `failed`）单独写测试；
  - 提示里说“可重试”“可继续”，就走完重试本身，并断言结果里是用户最后的选择；
  - 清空或覆盖屏幕上的状态放在内核接受之后，拒绝不能改变屏幕；
  - fake 要能给出真实实现的拒绝，并让测试在屏幕上有任务时触发；
  - 把“拒绝”固定成预期的测试，要写明拒绝之后用户还能做什么；答不出来就是缺口。
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
