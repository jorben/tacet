# 资源冻结与任务存储集成验证

状态：部分实现；完整 MVP 和 M0 准入尚未完成。本轮没有冻结加密 SQLite 选型，集成测试使用本地未加密临时测试库。

## 冻结资源隔离

`refs.Grant.Freeze` 在 stream.end 后先检查执行尝试，再按观测到的密文长度进行有界复制，目标为插件未获得句柄的宿主专有文件。复制同时计算摘要，随后用该资源密钥验证全部 AES-GCM 段（包括末段）、设置只读权限并刷盘，才登记冻结资源。原资源身份和密钥保持不变，因为只是复制密文，没有再次执行加密或复用 nonce。

这修正了原实现的不足：chmod 不会撤销已经继承的可写文件描述符。现在插件保留的旧描述符仍指向旧文件，迟到的追加不会影响冻结资源。实际冻结文件的存储篡改仍由 Verify 检测。测试覆盖旧句柄追加不改变候选、明文/截断/中间段篡改不能冻结、失败冻结清理半成品。

授权索引从 attempt_id 改为完整 AttemptRef（job、attempt、owner generation、job version）。不同任务/版本的同名 attempt 不会被相互吊销；已吊销尝试不能重新获取引用。Handoff 在同一互斥区复制密钥与读取权限信息，避免和 Revoke 清零并发时的数据竞争。

仍需：授权目录持久化、进程生命周期与 revoke 联动、磁盘累计配额、冻结时副本的空间预留、平台独立数据管道方案、Windows 实机，以及删除失败的清理重试。当前 Freeze 的副本计入后续资源预算设计，不能由它推断批次累计磁盘预算已实现。

## 工作区与真实事务

`workspace.BackendFunc` 适配 store.DB.Begin。提交回调中的 `store.InTransaction` 解包同一个数据库事务，模块仓库无需另开连接。`workspace.AdmitTx` 验证传入事务属于该工作区，并在该事务内核对执行代次、attempt、job version；阶段结果与准入可以原子提交。

原先直接在 Commit 内调用 Admit 会再次获取串行锁；新集成路径明确使用 AdmitTx。AfterCommit 回调在持久化完成、释放串行锁后运行，允许调度动作再次提交；并发回调自身须线程安全。测试使用真实 SQLite 验证同事务准入/更新/发件箱、旧版本拒绝、提交后回调重入不会死锁。

## jobs 拥有的持久化数据

`jobs.Module` 注册 jobs_batch、jobs_job、jobs_stage 和相关索引。部分唯一索引限制每种批次类型最多一个未结束实例，快捷任务和普通批次可以并存，不能被新建操作替代。批次 confirmed 标记冻结成员；成员列表用有上限的 keyset 分页查询。

`BeginStage` 在执行前保存随机 attempt、当前代次、任务版本和阶段记录。`CompleteStage` 检查版本/attempt/阶段记录，仅在 running 时接收结果，在同事务里写完成状态、产物引用、任务迁移、发件箱和提交后派发。重复结果被拒绝，不再产生事件或计数；用户控制命令携带 expectedVersion，变更时废弃原尝试。

`RecoverMember` 对已完成阶段不作重写，中断尝试标为 discarded，任务先呈现 paused；恢复到原阶段必须显式继续。取消中的任务在确认旧进程被回收后转 cancelled。生命周期调用方仍必须先回收上一代进程树并执行数据保留清扫。FinishBatch 只有在成员全部终止时释放活动批次类型槽位。

测试涵盖真实数据库的双类型并存、重复结果、结果回滚不触发派发、旧版本控制命令、暂停后迟到结果、冻结成员拒绝新增、有界分页、终止后释放槽位，以及关闭/重新打开数据库后执行代次推进、阶段/状态保存和暂停恢复。

ConfirmBatch 已实现同事务逐页确认成员：任一成员尚在预检时整次回滚，锁定成员转密码阻塞，已就绪成员在提交后排队，成员集合以 confirmed 标记冻结。测试覆盖先修改一个成员后遇到未完成预检、整个事务及派发钩子回滚，随后完整确认只派发就绪成员。app 仍须在同一用例内冻结快照与授权，因此不能将仓库函数当作完整 Start 用例。

snapshot.Repository 已按版本持久化内容与五分面摘要，加载时逐项验证；jobs_batch.snapshot_version 固定批次版本。真实事务测试覆盖快照保存、绑定和 ConfirmBatch 之后模拟授权失败，三者一起回滚；新增快照不会静默重绑正在运行的批次。

query_job 与 query_count 已由 JobStateChanged 增量维护。store.Consume 把投影变更与消费者游标前移放在同一事务，投影经 jobs 的只读接口读取事实。测试让第一个文件投影成功、第二个读取失败，断言汇总与游标一起回滚；重新消费和重复版本事件不重复计数，成员列表 keyset 分页限制最多 200 项。

仍需：配置/插件引用和发布授权、断点部分数据与 Purger 联动、调度预算和高水位、取消进程回收、Finding/例外/覆盖/交付读模型、后台事件分发和节流订阅、加密生产数据库及 CLI/Wails 接线。本轮只是持久化原语及其集成证据。

## 迁移拒绝路径修正

此前 Migrate 会在读取 store_schema 版本之前创建发件箱表，这会在拒绝未来格式时留下副作用。现在先只读检查 sqlite_schema 和模块版本，取得备份后才在迁移事务内创建核心表及模块表。未来版本拒绝不创建新表；首次迁移失败不保留核心表。对应回归测试直接检查 sqlite_schema。

## 检查入口

```sh
go test -race -count=1 -timeout=30s ./internal/refs ./internal/workspace ./internal/jobs ./internal/store ./internal/archtest
```

加入快照和查询集成后的本轮命令：

```sh
go test -race -count=1 -timeout=40s ./internal/jobs ./internal/query ./internal/store ./internal/workspace ./internal/refs ./internal/snapshot ./internal/archtest
```

该命令通过 132 项（含子测试）。完整 check、Go race 全套、前端测试/构建和 Windows 任务仓库交叉编译另行执行并记录；Windows 编译不能替代实机验证。

## 平台密钥库适配器（task-3 增量）

`internal/platform/secret*.go` 提供 `Secret`（Save/Load/Delete），`internal/keys/osstore.go` 的 `OSUserKeyStore` 把它接成 `UserKeyStore`。macOS 经 Security.framework 通用密码条目存放，可访问性为仅本机、设备解锁时可用；Windows 以 DPAPI 加密后存 `%LOCALAPPDATA%\Tacet\secrets\<service>\<account>.dpapi`，并把服务名与账户名作为熵；其他平台一律 `SECRET_UNAVAILABLE`。`Save` 对已存在条目返回 `SECRET_EXISTS`（映射为 `KEY_EXISTS`），避免替换用户密钥导致全部数据不可读。读取后 C 侧副本清零。

验证：`go test ./internal/platform ./internal/keys`（darwin，真实登录钥匙串，唯一服务名并清理；无钥匙串访问的会话会让测试失败而不是跳过，生产代码也不回退）。变异：去掉重复条目映射，测试失败。

局限：只验证了本机一个 macOS 版本与登录钥匙串；没有锁屏、钥匙串被锁、无图形会话（如 SSH）时的行为实测；Windows 仅 `go vet` 交叉编译；Go 无法保证清零不可变拷贝与运行时内部副本；未与签名、公证后的发布版应用的钥匙串访问组交互验证。

## 工作区生产装配（task-3 增量）

`workspace.OpenManaged(ctx, dir, Managed{UserKey, Registry, Versions, Create})` 的顺序：① 取 OS 锁并持久化新代次（之后的一切都在独占所有权下进行）；② `store.SweepBackup` 处理上次崩溃遗留的备份；③ `keys.Open` 与 `keys.DirWrapped`（`<dir>/keys/<kind>.<id>.key`）解开固定 id `workspace` 的工作区密钥；④ `store.OpenEncrypted` + `store.Open`（带 `FileBackup`）；⑤ 以该库作为提交后端。关闭顺序为数据库、密钥管理器、锁。任一步失败都会释放此前取得的全部资源。

安全约束：**库已存在而密钥缺失时拒绝**（`WORKSPACE_KEY_MISSING`），即使 `Create=true` 也不重建用户密钥或工作区密钥，避免出现读不了旧数据的新密钥层级；未带 `Create` 时不存在的工作区直接失败，不创建库或密钥。

清扫规则：迁移是单事务，主库只会是旧状态或新状态。遗留备份时主库能打开 → 备份过期，删除；主库打不开 → 校验 HMAC 后恢复；HMAC 不符 → 不使用备份、保留现状并报错，由用户处理。

验证：`go test ./internal/workspace ./internal/store ./internal/keys`（darwin/windows 构建标签；用内存用户密钥替身，真实钥匙串由 platform 测试覆盖）。变异：密钥建在已有库旁、用户密钥被重建、无 Create 也创建工作区、跳过清扫、失败打开不释放资源，均被抓住。

局限：`SweepBackup` 删除旧 WAL/SHM 的步骤无测试覆盖（难以构造“恢复后仍有可应用的旧 WAL”）；没有对备份或恢复进程中途终止的真实崩溃注入，只模拟了崩溃后的磁盘状态；`Versions` 仍由调用方（jobs）提供，OpenManaged 与真实 jobs 仓库的装配留给 task-5；Windows 仅交叉编译；用户密钥层的丢失没有恢复途径（设计上即不可恢复）。

## 受控引用的持久化目录（task-3 增量）

`refs.Manager.UseCatalog(NewSQLCatalog(workspace.Commit))` 把已完成资源（id、作业、相对路径、版本、摘要、大小）写入 `refs_resource`（模块 `refs`，前缀 `refs_`）。路径在资源目录内时只存文件名，工作区可整体移动。

状态流：`open` →（Freeze 在锁内复制并校验全部 GCM 段）`sealing` →（目录提交成功后）`frozen`。`sealing` 不可读，所以重启前后“可读”与“已在目录”一致。目录写入在 `refs.mu` 之外：`workspace.Commit` 的提交区间可能调用 `Revoke`（需要 `refs.mu`），在锁内提交会与之互相等待。目录失败时记录和密文文件一起删除，返回 `RESOURCE_CATALOG_FAILED`。

`Recover(ctx)`（只能在无任何资源时调用一次）：加载目录；文件缺失、非常规文件或大小不符的标记为损坏，列入 `Missing`，之后读取、`Digest`、`Verify` 均失败；资源目录里没有目录行的文件（未完成的写、`.frozen-*` 临时文件）一律删除。`Purge(job)`：有进行中的写则拒绝；先删目录行再删文件，崩溃于两者之间只会留下下次 `Recover` 清除的文件。已丢弃（abandoned）的记录只从内存忘掉，不计数。`SetJobQuota` 按作业汇总已完成字节与进行中写入的上限。

验证：`go test ./internal/refs`（darwin/windows 标签的集成测试用真实加密工作区与 SQL 目录）。变异：孤儿不清理、损坏文件仍冻结、已完成字节不计配额、目录失败被忽略、在 `refs.mu` 内写目录（死锁）、等，均被抓住；其中“损坏文件仍冻结”和“已完成字节不计配额”最初存活，补强断言后才被抓住。

局限：Recover 只比对大小，不重算摘要（摘要由 `Verify` 在使用前核对，全量重算对大资源代价高）；资源目录与数据库不是同一事务，崩溃窗口靠 Recover 对账（孤儿文件删除、缺失文件上报），已知窗口：文件已冻结但目录行未提交时，文件作为孤儿被删除（该资源需重算，不会被当作完成）；进程回收联动、真实插件加密读写吞吐、Windows 实机未验证。

## 受租约约束的出站 HTTP 客户端（task-3 增量）

`netgate.Lease.HTTPClient(Options{Hosts, TLS})` 是应用访问网络的唯一入口（调用方尚未接入）。只有 `Network` 租约可创建，`Processing`/`Maintenance` 租约返回 `ACTIVITY_KIND_INVALID`；由于处理租约与网络租约互斥，处理期间根本拿不到可用客户端。目标限制：仅 https、URL 不含用户信息、主机必须在精确主机名白名单内（不含端口，大小写不敏感，空白名单拒绝一切），重定向每一跳重新校验，最多 5 跳。租约释放会取消在途请求和仍在读取的响应体（返回 `ACTIVITY_LEASE_RELEASED`）并关闭空闲连接，因此“租约已释放而传输仍在继续”不会发生；`Gate.Acquire(Processing)` 在下载进行中被拒绝，释放后可获得。传入的 `tls.Config` 被克隆，避免 HTTP/2 初始化修改调用方对象（共用同一配置时 `-race` 曾报数据竞争，已修复）。

验证：`go test -race ./internal/netgate`（本地 httptest TLS 服务器，主机 127.0.0.1）。变异：释放不取消在途工作、非 Network 租约可建客户端、白名单被忽略、响应体错误不映射，均被抓住。两个变异存活并判定为等效：去掉请求前的租约准入检查（释放时上下文已取消，请求同样不会发出）；去掉 CheckRedirect 中的校验（每一跳同样经过 RoundTrip 的校验）。二者保留为纵深防御。

局限：只有本地回环主机的测试，没有真实公网、证书固定、代理环境、HTTP/2 长连接以及 Windows 实机的行为；`ProxyFromEnvironment` 沿用系统代理，代理本身不在白名单约束内（连接的是代理地址）；没有响应体大小上限和总超时，调用方须自行设置；下载的完整性校验、签名与安装属于 task-9。

## 保留期限与清理编排（task-3 增量）

`retention.Service`（模块 `retention`，表 `retention_grant`、`retention_step`）按模块设计 5.6 编排。

- **授权与期限**：`Grant(owner, days)` 记录用户明确保存或恢复的时刻，`days` 取 1 至 36500（上限只为防止溢出，需求没有规定上限，产品默认 7 天由调用方传入）。再次 `Grant` 是唯一的续期方式；`Usable` 与 `Sweep` 不改变时间，后台心跳不续期。`Usable` 对没有授权、已标记过期、已过截止时间都返回 false，并以调用时的时钟判定，所以过期与清扫之间没有可用窗口，时钟被拨回也不会让已标记过期的授权复活。
- **Purge(job)** 的顺序：有发布进行中则拒绝（`RETENTION_PUBLISH_IN_PROGRESS`，什么都不动）→ 加密擦除任务密钥 → 依次执行各模块注册项（按名字排序）→ 撤销登记引用。注册项 `Registration` 二选一：`Tx`（沿用已有的 `Purger.Purge(ctx, tx, job)`，在提交区间的一个事务内，失败整体回滚）或 `Func`（文件类清理，自管提交，如 `refs.Manager.Purge`，因为提交区间不得做大块 I/O）。每步结果写入 `retention_step`（`done`/`failed`），可通过 `Steps` 查看。密钥擦除失败时不执行任何其他步骤（密钥仍能解密时不删除数据）；其余步骤彼此独立，一个失败不阻止其他，最后返回 `RETENTION_PURGE_INCOMPLETE`。重试时跳过已完成步骤。全部成功后才删除授权和步骤记录，所以半途失败的清理一直可见。
- **Sweep**：对 `Jobs()` 返回的每个仍有数据的作业：没有授权、已过期、或有未完成步骤的作业都执行 Purge；到期的授权先标记再清理，即使清理失败，使用也已被阻止。有发布进行中的作业留到下次，失败的计入 `Failed` 下次再试。`Jobs` 须只列出仍持有数据的作业（已清理的不再列出），否则每次启动会重复处理（幂等但浪费）。

验证：`go test -race ./internal/retention`（darwin/windows 构建标签，真实加密工作区、真实 keys.Manager、真实 refs 目录与文件，假时钟）。变异：发布未等待、密钥擦除失败后继续、已完成步骤重复执行、失败被忽略而丢弃授权、续期不重置期限、过期标记被忽略、过期未先标记、半途清理不续做、等，均被抓住；其中 3 个最初只是编译失败，换成能编译的写法重做后才算数。

局限：`registry` 的 `Unpin` 在测试里用的是计数替身，没有与真实 registry 仓库联测；除 `refs` 外的模块 Purger（发现项、计划、复核、候选、报告等）还不存在，对应数据的清理要等这些模块实现时注册；任务历史条目“关联置空”未实现（历史模块尚无）；“停止并清理”“发布后清理中间数据”的事件触发（消费 `Published`）未接线，依赖 jobs/publish；没有对清理过程中进程崩溃的真实注入，只靠步骤记录与幂等重试；清扫未覆盖旁路文件（日志、诊断、临时目录）；Windows 仅交叉编译。

## 插件协议、SDK 与环境管理（task-4 增量）

**协议会话的敌对对端测试**（`internal/host/session_hostile_test.go`，进程内假插件经 `net.Pipe` 发送原始帧）：握手拒绝插件 ID、版本、协议号不符；请求携带完整尝试标识与字节/单元两维信用；提交进行中不发信用、提交完成后才发；单条超过字节信用在提交前被拒；他流条目（上一次尝试的迟到结果）、序号跳变与零序号、缺 `kind` 的条目、结束声明多于已收、结束属于他流、响应 id 属于他请求、错误响应、超过协议上限的帧、截断帧、非 JSON 帧均断流，且持久化回调从未被调用，会话此后不可再用；提交失败断流且不再发信用；取消发出带作业/尝试/代次/版本的 `task.cancel`，对端不再读取时也在约 2 秒内返回；截止时间写入请求并由宿主强制；他代次的请求在发送前被拒。

**SDK 运行时**（`python/tests/test_runtime.py`，13 项）：协作取消得到 `cancelled` 结束且工作槽随即释放；截止时间在插件内强制；超过字节信用的条目以 `RESOURCE_LIMIT` 失败；忙碌时拒绝第二个重任务（`WORKER_BUSY`）；未知方法是错误响应而非崩溃；超信用/他流信用/他尝试或他代次的取消/重复初始化/初始化前的任务、健康检查与取消/垃圾帧均使工作进程以状态 1 退出。

**`envs`**（模块设计 9.4，表 `envs_env`）：环境摘要由 OS 与架构、Python ABI、锁文件摘要、产物摘要派生，记录不能自称摘要；`BeginDeploy` 记为部署中，`Finish` 在解释器文件检查与调用方注入的探针（`envs` 不能启动进程，由 `host` 提供）都通过后才置为就绪，检查失败记为 Broken（返回状态而非错误，以便调用方提交这一结果），Broken 不能被 `Finish` 复活；`LaunchSpec` 只给就绪环境，且每次重新检查解释器仍是可执行的常规文件。启动规格为 `-I -m <模块>`，环境变量只有 `PYTHONDONTWRITEBYTECODE`、`PYTHONUTF8`、`PYTHONNOUSERSITE` 与最小 `PATH`（Windows 为 `SystemRoot`），不继承 `PYTHONPATH`/`PYTHONHOME` 等；工作目录必须是调用方给的存在的绝对目录；模块名与参数有格式检查。`MarkRedeploy` 把 ABI 或平台不同的第三方环境标为需重新部署（内建不动）；`Reclaim` 只删除无引用的第三方环境的登记，目录由 `RemoveRoot` 在提交后删除，且只允许在基目录之内（拒绝基目录本身、目录外与指向目录外的符号链接）。

验证：`go test -race ./internal/host ./internal/envs`；`python -m unittest tests.test_runtime`。变异：宿主 8 个（外流 ID、跳过流量准入、不核对插件 ID/协议、不核对结束序号、不核对代次、不等提交就发信用、不强制字节信用）；SDK 9 个（不强制截止时间/字节信用/忙碌/他流信用/取消身份/初始化前请求/重复初始化、泄漏异常文本）；`envs` 11 个。

发现并处理的问题：① 取消测试不稳定（单独运行稳定失败、`-race` 下偶然通过）：对端读完请求或信用帧时宿主的发送 `select` 可能尚未返回，此时取消会走"发送期间被取消"分支，关闭会话但不发取消消息；这是被测行为的真实特性（取消落在发送窗口内时依赖监督器回收进程树），测试改为先确认宿主进入接收循环并稳定等待。② 我先写的"忽略背压"测试没有意义（宿主单线程读取，超发的消息只会排队），改为断言提交期间不发信用。③ `Finish` 的初版在检查失败时返回错误，调用方回滚后 Broken 标记会丢失，改为返回状态。④ `resolveInterpreter` 初版有个让包含检查形同虚设的分支，改为如实只做字面检查。

局限：宿主与 SDK 的"无界预读"仅在这条流上论证，没有 100/500 MiB 级实测；`envs` 的探针只在 macOS 上用真实 DOCX 插件验证，Windows 的启动规格（`SystemRoot`、`python.exe` 路径）未实机运行；环境部署（uv、仅预编译 wheel、不执行安装钩子）、签名/公证的内建环境、引用接线（`registry` 的 `Referenced`）在 task-9 与装配阶段；环境摘要的 `ArtifactsDigest` 由谁、如何计算尚未实现；取消落在发送窗口时不发取消消息；插件 stderr 只计数；Python SDK 的 JSON Schema 与 Go 共用同一份嵌入文件，但没有自动对比两侧校验结果一致的差分测试。

**Go/Python 共用契约的差分向量**：`protocol/testdata/schema-conformance-v1.json` 含 81 条人工标注判定的向量，Go（`internal/host/schema_conformance_test.go`）与 Python（`python/tests/test_schema_conformance.py`）各自读取同一文件逐条断言。向量刻意覆盖两种实现易分歧处：写成 1.0 或 1e3 的整数、2^53-1 边界与 uint64 之外的整数、按码点计的 maxLength（128/129 个 emoji）、`unevaluatedProperties`、`oneOf` 的 id 类型、`additionalProperties`、枚举与固定消息。

**发现的真实分歧（已修）**：正则末尾的 `$` 在 Python 里还匹配结尾换行，而 ECMA-262 与 Go 不会，因此错误码 `PLUGIN\n`、带结尾换行的资源密钥与摘要会被 Python 放行、被 Go 拒绝。SDK 现在覆盖 `pattern` 关键字，把末尾的 `$` 视为 `\Z`（所有随包 Schema 的 pattern 都以 `$` 锚定）。去掉该覆盖则 3 条向量失败。

局限：81 条向量是人工挑选，不是对 Schema 的穷举或随机差分；转义 `\$` 结尾的 pattern 不处理（现有 Schema 没有）；除末尾 `$` 外的其他正则方言差异（如 `\d`、`\w` 对非 ASCII 的处理）未被向量覆盖，现有 pattern 都只用显式字符类。

## findings 候选存储（task-6 增量）

模块 `findings`（表 `findings_attempt`、`findings_result`、`findings_finding`）。**尝试先开放、封存后才可见**：`Add` 在一个阶段尝试内可多次调用（逐单元提交），所有查询只看已封存尝试的命中，所以失败、取消或崩溃的阶段留下的部分结果不会被当成完整结果；`Discard` 删除未封存尝试的命中（恢复时由拥有者调用），已封存的拒绝删除。`Seal` 幂等，**检测结果版本只在该尝试至少新增一条命中时递增**（没找到新东西的阶段不使既有决定失效），并发出 `FindingsChanged`；零命中的阶段也能封存。尝试一经登记，其任务、批次、阶段不得改变。

**不存原值**：`Add` 的 `Value` 仅用于计算同值 HMAC（批次密钥派生，`keys.GroupHMAC`），调用返回前清零；表中没有任何原值列，也没有无密钥哈希。测试逐列扫描所有行确认标记值与明文不出现，不同批次的同一值 HMAC 不同。

**查询**：`Page` 按 id keyset 分页；`Groups` 按（类别、来源、字段、模板、同值）聚合并 keyset 分页，返回命中数、涉及文件数与最小 id 的代表样例。未限定任务或批次的查询被拒绝。

验证：`go test -race ./internal/findings`（真实 SQLite 迁移与真实 `store`）。变异：未封存可见、空阶段递增版本、不校验条目、HMAC 不分批次、调用方缓冲不清零、封存不幂等、已封存可丢弃、代表样例不取最小 id、文件数按命中数计、分页重复游标行、分页上限失效，均被抓住；其中 3 个最初存活或仅编译失败（文件数、HMAC 分批次、分页死循环靠超时才失败），补测并加迭代上限后才算数。10 万候选规模：`TACET_SCALE=1 go test -run Scale -v ./internal/findings`。

局限：`AddManual` 依赖 `preview` 的位置校验，留给 task-7；分组聚合每次对全部可见命中分组，未做增量维护，百万级未测；规模数据来自未加密库与开发机；模板结构键与字段路径由抽取阶段提供，当前没有生产者；`findings` 尚未接入 `retention` 的 Purger 注册与 `extract` 阶段。

## 抽取检测阶段（task-6 增量）

**协议**：`format.schema.json` 增加 `ExtractParams`（输入为只读资源引用）、`TextUnit`（单元 ID、部件、段落序号、至多 1 MiB 的文本、`visibility` 为 visible/hidden/revision、源映射 = 部件 + 段落序号）、`PartState`（部件、类别 text/other/unknown、状态 inspected/not_inspected/unsupported/failed、原因码）、`ExtractComplete`（声明单元数与部件数）。Go 与 Python 共用同一文件。

**DOCX 插件**（`extract.py`）：为每个包部件上报状态；含文本的部件逐段产出单元（空段落不产出但保留序号，写入端据此寻址）；隐藏与修订单独标注；读不了的部件记为 `failed`（缺口）但不使整个文件失败；未知条目记为 `not_inspected`；最后发出带计数的完成标记。

**`extract.Pipeline`**：每个单元先在提交区间之外检测（规则 + 模型），正文写入加密资源文件，随后在**同一事务**内写单元元数据、命中与账本，提交后才由 `Session.Stream` 向插件补额度。完成判定与失败清理见 INV-02 证据。数据库只存引用、位置与计数：测试逐列确认单元文本、手机号与正文不进入任何表，正文文件无明文。

验证：`go test -race ./internal/extract ./internal/coverage ./internal/findings ./internal/detect`；`python -m unittest tests.test_docx_extract`。变异（pipeline 13 个、账本 6 个、DOCX 抽取 6 个）均被抓住；其中一部分最初存活：失败流被计数巧合掩盖、完成标记重复、提交失败后正文泄漏、账本新尝试沿用上次的完成标记、同一部件重复上报，均补用例后才算数。

局限：只有 DOCX 文本子集有抽取处理；单元正文 `FileBodies` 的任务级密钥派生与 `retention` 的 Purger 注册尚未接线；`extract` 的检查点保存与恢复（`jobs.SaveCheckpoint`）未接入；预检与密码解锁（`preflight`）、页码/章节定位、表头检测上下文、补充扫描与验证再检测（同一 Pipeline 用于候选）未做；规模（数百份文档）与 R1 资源曲线未测；模型每单元一次协议往返，没有跨单元微批。

## 预检（task-6 增量，部分）

`platform.Identify` 给出卷内文件标识（设备、索引、硬链接数，跟随符号链接；darwin 用 `Stat_t`，Windows 用 `GetFileInformationByHandle`，后者仅交叉编译）。`preflight` 按架构边界不能导入 `platform`，所以 `Identify` 作为函数注入，`app` 装配时接入。

`Inspect`：真实路径、是否经符号链接解析、快速指纹；`Changed`：与检查时是否一致。`CheckTarget`：输出目标不得是源本身。`Decide`：纯函数，把探测结果判成 preflighted / locked / unprocessable（带实际值与上限）。`Unlock`：见 FR-09。

验证：`go test -race ./internal/preflight ./internal/platform`。变异 13 个；其中大小写变体分支最初存活，分析后发现是死代码（目标存在时标识已识别别名，不存在而仅大小写不同说明是区分大小写的另一个文件，拒绝反成误报），删除并改测。

局限：真实解密、`format.probe` 协议与各插件的探测实现、完整摘要后台计算、批次汇总（格式分布、图像默认处置、命名预览、空间预算）、预检阶段的派发与 `jobs` 接线均未做；在区分大小写的卷上没有实测。

## 密码解锁（task-6 增量，DOCX）

**协议**：`format.schema.json` 增加 `UnlockParams`（只读输入引用、加密追加写输出引用、`password` 标 `x-secret`/`writeOnly`）与 `UnlockDone`。SDK 新增 `inherited_writer`（只接受加密、追加写的宿主句柄）；错误码增加 `PASSWORD_INCORRECT`、`ENCRYPTION_UNSUPPORTED`、`NOT_ENCRYPTED`、`INPUT_CORRUPT`。

**宿主**：`Session.StreamSecret` 与 `host.Secret`。密码从不进入 `params` map：请求先按无密码形态序列化并通过 Schema 校验，再在一块按最坏转义容量预分配的缓冲里拼入密码字段，写出后清零；字段名、长度（≤1024 字节）、UTF-8 与是否遮蔽已有参数都在占用请求 ID、发送任何内容之前校验（否则发送路径上的失败会关闭整个会话——这是一个测试暴露的问题）。插件对完整消息用自己的 Schema 校验。

**插件**（`unlock.py`）：msoffcrypto-tool 解密。关键发现：(1) 库在写出之前才检查结果是不是 zip，错误密码会先写出垃圾，所以改为 `verify_password=True` 后解到内存缓冲，`verify_integrity=True` 通过且确认是 zip 才写入输出；(2) 损坏的 EncryptionInfo 会抛 `ExpatError`，统一兜底而非列举异常；(3) 口令已验证后的失败报 `INPUT_CORRUPT`，不报密码错误（否则让用户白白重输）；(4) 库自带的 CFB 写入器在不足 4096 字节的流上写出错误数据，测试夹具因此补足体积并注明由库自己加密、不是 Word/WPS 样本；(5) SDK 的 `ResourceReader` 缺少 olefile 探测的 `closed` 属性，内存 `BytesIO` 测试掩盖了这点，是真实进程测试暴露的，已补并加走真实读写器的测试。

**Go 适配器**：`preflight.PluginUnlocker` 实现 `Unlocker`：校验输入引用与两个引用的模式后才发送；只接受恰好一条合法 `unlock_done`；仅在流以 completed 结束后调用 `Seal`；`failed` + `PASSWORD_INCORRECT` 映射为 `ErrPasswordWrong`，其余一律 `ErrUnlock`。

验证：`python -m unittest tests.test_docx_unlock`（10 项，含全文件逐位损坏扫描、真实 SDK 读写器）；`go test -race ./internal/host ./internal/preflight`（含假插件 15 场景与真实 DOCX 插件进程）。变异：unlock.py 9 个（2 个等价/冗余：失败后空数据仍被后续检查拦住；zip 检查被完整性校验覆盖，另有"HMAC 有效但载荷不是 zip"用例）、host 10 个、PluginUnlocker 9 个（2 个等价：外层 `Unlock` 已拦空引用；仅状态判断的冗余）。假插件测试曾偶发失败，原因是宿主拒绝条目后关闭会话而假插件仍写入，属测试自身竞态，已容忍并重复 300 次通过。

局限与挂账：Word/WPS 真实加密样本；大文件内存（msoffcrypto 整包入内存，输入上限 256 MiB）；PDF 解锁；`format.probe`；解密副本登记为任务输入与原件变化失效；GUI/CLI 输入；Windows 句柄继承路径仅交叉编译。

## 格式探测（task-6 增量，DOCX）

**协议**：`format.schema.json` 增加 `ProbeParams`（只读输入引用）与 `ProbeResult`（`format` 为标识符、`encryption` 为 none/password_required/permission_only/unsupported、页数、内嵌媒体数、嵌入对象数、展开体积）。插件只报告所见；是否接受由 `preflight.Decide` 按注册表与上限判定。

**DOCX 探测**（`probe.py`）：先看签名。OLE 头：用 msoffcrypto 只读加密头，OOXML 的 agile/standard 加密为 `password_required`，其他方式为 `unsupported`，非 OOXML 或未加密的 OLE 为 `unknown`。ZIP 头：经 `safezip` 校验后做 OPC 清单，要求有 Word 主部件，否则 `unknown`；损坏或超限的包使探测失败而不是给出格式结论。

**共用向量的收获**：新增 9 条 Schema 一致性向量后，Go 立即拒绝了我写的 `\Z` 结尾模式（RE2 不支持），Python 却接受——两侧只有共用向量才暴露这一差异。已改为与现有 Schema 一致的 `$`，两侧对带尾换行的 format 都拒绝。

验证：`python -m unittest tests.test_docx_probe`；`go test -race ./internal/preflight`（含 `TestRealDOCXProbeThenDecide`）。

局限：DOCX 页数为 0；PDF 与文本类插件无 probe；探测无单独的阶段预算。

## 文字型 PDF 插件（task-6 增量）

入口 `tacet_format_pdf`：`format.probe`、`format.unlock`、`format.extract`。

**probe**：先看 `%PDF-` 签名；pypdfium2 以无密码打开，`FPDF_ERR_PASSWORD` → password_required，`FPDF_ERR_SECURITY` → unsupported，打开成功但安全处理器在用 → permission_only；统计页数、图像对象与嵌入文件。损坏是探测失败，不是格式结论。

**unlock**：与 DOCX 同样的先验证后写出：带密码打开 → `FPDF_REMOVE_SECURITY` 另存到内存缓冲（输入上限 256 MiB）→ 复开无密码、页数一致、无安全处理器 → 才写入加密输出资源。

**extract**：每页一个文本单元（页文本按 PDFium 顺序逐字取出，单元内码点位置即页内字符索引，供后续按字符框定位；超过 262144 码点的页在最后一个换行处确定性切分，来源映射为 `page/N` + 块序号）。未读取的内容各报一个 `not_inspected` 部件，类别一律 `unknown`：`page/N/images`、`page/N/annotations`（非链接批注）、`document/metadata`、`document/outline`、`document/attachments`。不可靠的页（Unicode 映射缺失、字符框非有限）报 `failed`，不产生单元，也不使整份文件失败。

**教训**：第一版把图像、元数据标成 `other`，Go 端的覆盖评估把它们当作范围外——一个会让图片里的文字被静默排除的缺口，只有走完整个流水线才暴露；`other` 的含义是"不含文本"，不能用于"我没读"。

验证：`python -m unittest tests.test_pdf_probe_unlock tests.test_pdf_extract`（22 项）；`go test -race ./internal/preflight ./internal/extract`（真实 PDF 进程：探测+判定、解锁正确/错误密码、抽取+检测+覆盖）。变异 21 个：18 个被抓住；3 个存活均为冗余防御（复开后再查安全处理器——无密码能打开已隐含；页数比较——无构造手段让两者不同；首次打开 unsupported 的提前分支——随后带密码打开同样得到 unsupported）；"不按换行切分"最初存活，原因是测试文本恰好 10 字符，换文本后被抓住。

局限：不区分不可见文字（渲染模式 3，如 OCR 层）；不检查矢量轮廓文字；JavaScript、XFA 不读；旋转页的文本位置未经回写验证（回写属 task-8）；批注、元数据、书签、附件的文本均未检测（作为缺口交给策略处置）；DOCX 之外没有 Word/Acrobat 生成的真实样本；PDF 的 RC4 夹具外未测 AES-256 (R6) 加密。

## 抽取数据的保留期清除与任务级密钥（task-6 增量）

- `extract.Repository.Purge(tx, job)` 删除该任务全部单元元数据（含未封存尝试）；`extract.JobBodies(root, job, derive)` 把每个任务的正文放进自己的目录，单元正文的密钥由任务密钥派生（`derive` 注入，`extract` 不导入 `keys`），`PurgeBodies(root)` 幂等地删整个任务目录。任务 ID 与根路径先校验，`../`、斜杠、空串一律拒绝，清除 `j1` 不会碰到 `k2` 的正文。
- `retention.PurgerFunc` 让 `findings.Purge`、`coverage.Purge`、`extract.Purge`（签名不带 ctx）无需包装类型即可注册。
- 集成测试用真实 `retention.Service` 与四个注册项：清除后 7 张表为空、任务目录消失、另一任务的正文保留、任务密钥擦除 1 次，擦除后同一份密文无法读取；正文文件中不含手机号或原文。
- 变异 10 个：8 个直接被抓，2 个（空 job 清除、目录 0755）最初存活，补用例后被抓。

**task-6 整体仍未做**：阶段调度接线（`preflight`/`extract` 作为 `sched.StageRunner` 派发，属 `app` 装配，task-10）；抽取阶段的检查点恢复（当前中断即整个尝试丢弃并重做，失败闭合，但长文件会重来）；text/CSV/JSON/XLSX 插件的 `format.extract`（扫描器已有，协议入口属 task-8 的格式准入）；批次预检汇总（BUX-01）；解密副本登记为任务输入版本与原件变化后的失效；真实语料的逐类别评测与校准（外部证据，挂账）。

## 决策层持久化（task-7 增量）

**findings**：`AddManual`（独立已封存尝试、按 request 幂等、立即提高结果版本）、`ByIDs`、`Count`（与 `Page` 同谓词）、`Filter.Units`（视区）。

**review**（`review_*` 表）：单项决定、两阶段批量决定（MD-11）、`Effective`（单项>批量、后写胜、待提交/已撤销忽略、按单元+文本版本+区间+类别匹配，因此同值新位置不继承）、`ImpactOf`、`Replace`（只撤销范围内单项）、撤销为新记录、`DropPending`、`AddManual`、批次补充词表（加密，迁移 v2）、复核位置、`Purge`。决定版本按任务单调递增。

**exceptions**（`exceptions_*` 表）：`Sync`、`Items`/`Groups`（键集分页、聚合）、`Decide`/`DecideGroup`（所见计数冻结）/`Revoke`、`Decisions`、`Open`、任务级版本、`Purge`。存储标识为 `ItemIDFor(job,…)`：只由类型和范围键定的标识会在不同任务间撞主键，这是写测试时暴露的。

**policy**（`policy_*` 表）：`Service.Run` 在短事务中分页读取命中、生效决定、例外决定与覆盖台账（无台账或未完成枚举 → `ErrCoverageIncomplete`，不当作缺口列表），计算后在**同一事务**核对检测结果版本和决定版本（复核+例外之和），一致才写入处置结果并 `exceptions.Sync`；不一致整体重算，次数用尽返回 `ErrInputsChanged` 且不落库。`Simulate` 只算视区内目标，草稿决定只作用于本次调用，不写库。`Dispositions` 分页供 plan 读取。

**preview**：`Validate`（过期/越界/他任务/不可靠位置/坐标映射结果校验）、`SafeText`（去控制字符与双向覆盖，限长；界面以文本渲染）。

**plan**：`Widen` 增加 `MaxPatches=1` 硬限制。

验证：`go test -race ./internal/{findings,review,exceptions,policy,preview,plan}`。变异：review 约 30、exceptions 约 35、policy 阶段 20、preview 14、plan 若干，存活者逐个分析：补用例（Replace 误撤销跨范围批量、检测版本未校验、阈值边界、区域无容器、版本递增）、删死代码（代理项分支）或记为等价（无台账分支被 Assess 同样拦截、版本和中的 perJob 子句）。

局限：`format.preview` 协议与插件侧选择映射/模拟叠加；向受影响成员派发补充词表扫描、候选关联（导出后新命中与既有决定的关联属 task-8 的 validate）；阶段调度与 `app` 装配；GUI；Windows 仅交叉编译；规模：`TACET_SCALE=100000 go test -run TestScaleOfOneResolution ./internal/policy` 实测 10 万命中（1 万单元）——写入 1.3 s、全量批量决定 1.0 s、一次处置 0.9 s（普通 SQLite，未含 Adiantum 加密开销，未含 Windows）。`policy.read` 把全部命中读入内存，10 万条下耗时不是问题，内存曲线与更大规模未测。

**实测抓到的缺陷**：带类别过滤的 `findings.Page` 每页 50 ms（无过滤 0.3 ms），10 万条走完 500 页要 25 s，批量决定因此需 26 s。原因是规划器选了以 category 开头的索引再对全部匹配行做临时 B 树排序，每页重做，总体二次增长；对类别/来源加 `+` 禁用索引后降到 0.17 s / 1.0 s。回归测试用 `EXPLAIN QUERY PLAN` 断言不出现 `TEMP B-TREE`（确定性，不依赖计时；撤销修复即失败）。此前 findings 的规模测试（页 0.4–12 ms）只覆盖了无过滤分页，漏了这个路径。

## 导出、验证、发布与报告（task-8 增量，进行中）

**协议**：`format.schema.json` 增加 `PlanLine`（计划按行 JSON Lines：文本操作含部件、段落/块序号、码点区间、替换文本；部件操作）、`WriteParams/WriteItem`（op_result、part_fate、write_complete）、`ValidateParams/ValidateItem`（input_part、output_part、op_check、unverified、validate_complete）；共用一致性向量 +20 条，另有 `protocol/testdata/regions-v1.json`（区域几何，Go 与 Python 同判）。错误码增加 `FORMAT_UNSUPPORTED/OUTPUT_LIMIT`。

**plan**：`Encode`（计划→JSONL，替换文本由渲染器给出，单个可靠源片段，区域操作拒绝）、`Renderer`（删除/占位符/部分掩码；掩码覆盖少于 3 个码点则整体遮盖）、`Locator`；`Widen` 限一次。**refs**：`Grant.HostWriter`，宿主写入自己授予的资源（计划），随后冻结。

**export**：`Stage`（计划写成冻结加密资源 + 输入只读/计划只读/候选只追加三份授权）、`Writer.Write`（严格核对 write 流）、`Service.Export`（冻结候选、同一提交内核对所有权并登记候选与部件去向，失败撤销全部授权且不留登记）。

**DOCX 插件**：`write.py`（逐段应用编辑；移除部件同步改写 Content_Types 与关系；未改动部件逐字节复制；不在文本子集内的部件整体失败并给出原因码）、`validate.py`（独立重开候选；逐操作对齐判定 `judge`：已执行/残留（含号码末几位残留）/无法对账，文本段整体与计划不符不会被报告为成功）。**PDF 插件**：`write.py`（栅格化输出，涂黑区域由与 Go 同一几何得出；页转换、元数据/书签/附件移除；含图像页拒绝并给出原因）、`validate.py`（逐像素核对每个操作区域已涂黑，页尺寸不符不会被当作黑色通过）。

**validate**：`Runner`（严格核对 validate 流）、`Evaluate`（对账、残留/未对账、关联、结论）、`Associate`（候选坐标→输入坐标，保留文本/生成文本/跨界拆分/已知命中去重）、`Service.Run`（同一提交内写入新命中、导出后发现项与结果；未完成记 not_run 且不落库）；`exceptions.SyncPostExport` 使导出后发现的例外不被普通处置同步误关。

**publish / report**：见 FR-30、MD-16、FR-36。

验证：真实 DOCX 与 PDF 进程完整闭环测试（抽取→计划→回写→验证→再检测：全部涂抹通过；漏做一个得到新命中并定位到输入坐标；占位符触发规则→复核项而非命中；未涂黑的栅格页→残留；候选无法重开→损坏）。变异约 200 个，存活者逐个分析：补用例（judge 的缺失替换、页尺寸、书签、阈值边界）、记为等价/冗余（后续计数同样拦截）或删除。

局限与挂账：
- 含图像、批注的 PDF 页被栅格化路径拒绝（原因码明确，不静默放过）；DOCX 仅文本子集（表格/布局词汇之外的元素、隐藏文字、修订整体失败）；
- 无 Word/WPS/Acrobat 打开验证，无真实 Windows；
- 导出前按分辨率估算栅格化 PDF 体积、候选空间计入 ResourceClaim 未接线；
- 阶段调度（jobs 状态迁移、自动扩大后回到导出、Published 事件消费者生成报告）与 `app` 装配未做；含源路径的加密定位索引未做；
- 规模（数百份文档）与资源曲线未测。

## 文本类格式插件（task-8 增量）

共用内核 `tacet_plugin_sdk.unitkit`：格式适配器只提供单元迭代（`units`）、编辑（`edits`）、应用（`apply`）与部件枚举（`enumerate`），probe/extract/write/validate 的协议严格性写在一处。

- **TXT/MD/log**：单元为一行（不含换行符），序号 = 行号×64+分片（超过 16384 码点的行切片，最多 64 片，更长则文件失败）；编码依 BOM、严格 UTF-8、GB18030 顺序识别；含 NUL（非 UTF-16）与 UTF-32 不接受。**已知限制**：极短的 GB18030 文本若恰好也是合法 UTF-8（例如单字“一”）会被读作 UTF-8，这是自动识别的固有歧义，有测试记录。
- **CSV/TSV**：单元为单元格（序号 = 文件内单元格总序），整格替换并保持原有引号；若替换使值以公式触发符开头，沿用 M0 的单引号保护，验证会把它判为“与计划不符”（不报成功）；定界符取首行最多者（平局取逗号），整个文件必须解析成功才被接受。
- **JSON/JSONL**：三个部件 `values`（字符串值，可编辑）、`keys`、`numbers`（7 位及以上数字的数值词素，如以数字存储的手机号/证件号）；`keys` 与 `numbers` 的改动需要方案授权（改键/改型），因此回写拒绝并给出原因码——命中其中会使导出失败，而不是被放过。
- **XLSX 值子集**：部件为文件的真实部件；单元为文本单元格（共享字符串单元格显示其共享串文本，编辑后变为内联字符串，无引用的共享串被清空）；数值单元格同样作为只读视图；公式、批注、绘图、表格、定义名称、外部链接等使文件不被接受。文档属性部件作为“未检视”部件报告，可被 part 操作整体移除（全删或全不删，不保留则整个写失败并给出 `XLSX_UNINSPECTED_PART_POLICY_REQUIRED`）。
- **验证**：`unitkit.validate` 独立重开候选，逐单元判定；内核在 `internal/validate` 的真实进程测试覆盖四种格式：全部涂抹通过、漏做一个得到新命中并精确定位回输入坐标（含同值分组）、不完整候选判残留（可扩大），XLSX 数值单元格命中使导出失败并给出原因码。
- 手机号等写入格式的**回写不改变字节以外的内容**：TXT 逐字节核对多种换行与编码。

**task-8 剩余挂账**：Word/WPS/Acrobat/Excel 实际打开验证与真实 Windows（外部证据，并入 task-2 挂账）；含图像/批注的 PDF 页与 DOCX 子集外元素按原因码拒绝（不静默放过）；XLSX 的发布前按分辨率估算体积、候选空间计入 ResourceClaim 未接线；阶段调度（jobs 状态迁移、扩大后回到导出、Published 事件消费者生成报告）与 `app` 装配未做，属 task-10；明文摘要在验证时绑定（当前发布时对冻结的、逐段认证的不可变资源做首遍流式摘要）。

## 模型资源、插件管理、安全更新与生命周期（task-9 增量）

**netgate.Fetcher**：租约内的下载客户端，Range + If-Range 续传，强 ETag；`models`、`update` 不导入 net/http。

**models**：清单 `ParseManifest`（严格、摘要覆盖后端与全部文件的名称/大小/哈希）、`ParseSigned`；`Service` 的逐模型状态机（待下载/下载中/已暂停/校验中/已安装/准备中/已就绪/可恢复错误）；下载→暂存→逐文件核对→原子登记→准备（由 worker 加载并跑固定样例）；暂停保留续传数据，退出等同暂停，启动时只恢复状态不联网；`CheckBinding` 在任务开始/恢复/切换方案时核对。测试中发现并修复：`SessionLost` 会把错误态覆盖成已安装；准备失败的模型被误判为未安装。

**update**：验签（`Verifier`，用途绑定）、发布清单、`Check/Download/ImportOffline/Apply`、`FirstRun`（见 SEC-15）。

**pluginmgr**：`Prepare`（取得→检查→待确认）、`Confirm`（部署→就绪检查→登记，失败回滚）、`Enable/Disable/Uninstall/Rollback`、规则包。`registry` 增加 `Check/References/Versions`。

**lifecycle**：`Starter`、`Stopper`、`Wake`、`Awake`、`DecideClose`。

验证：`go test -race ./internal/{netgate,models,update,pluginmgr,lifecycle,registry}`；变异约 150 个，存活者分析：冗余防御（更新包大小已被哈希核对覆盖）、等价（弱 ETag 以外的分支）、补用例（文件数/总大小上限、摘要含文件名、续传记录绑定文件、会话丢失不改错误态）。一个模型变异曾使测试无限等待（缺少超时），已按约定改为带 `-timeout` 重跑。

**局限与挂账**：
- **没有真实的第三方插件环境部署**：`Deploy`/`Probe`/`Git`/`Cleanup`/`Prepare`（模型加载与固定样例）是注入的函数，测试用替身；`envs` 的 `uv` 离线部署、沙箱内就绪检查、git 检出的真实进程尚未接线，需 app 装配（task-10）与 M0 的环境验证（task-2）；
- 离线插件安装只覆盖“本地路径复制”；依赖 wheel 默认需要联网下载，`Inspection.NeedsNetwork` 会提示，无网时部署会失败并回滚。随包携带 wheel 被 SEC-14 的规则拒绝，离线 wheel 仓库方案未设计；
- 真实 Wails 更新器、签名、公证均未做；系统关机/注销已接线（PF-15 证据），未经真实关机验证；托盘与睡眠/唤醒已接线（PF-15 证据），未经真实窗口验证；
- retention 的期限与单任务密钥擦除沿用 task-3 的实现与测试，本任务未新增，仅需在 app 装配时注册 review/policy/exceptions/export/validate/publish 的清除步骤（均已提供 Purge）；
- 未在 Windows 上执行。


## 应用装配、CLI 与桌面接线（task-10 增量，进行中）

**装配（`internal/app`）**：`Open`/`OpenWith` 取得工作区、恢复资源、登记内置插件与内置规则/方案/预设、启动调度与完成循环。六个阶段运行器（预检、抽取+检测、解析、导出、验证、发布）把真实插件进程（`host.Start`，`-I -m <module>`，环境白名单，按“工作区哈希+代次+序号”打标记）接到 `sched`/`jobs`：阶段完成经 `ws.AdmitTx` 与 `jobs.CompleteStage` 落库，应用侧后续（发布授权、报告、批次结束）在 `afterState`。`app` 因此是组合根，允许额外依赖 `host/refs/keys/netgate/platform`（`archtest` 同步放宽并注明）。

**用例（`Core`）**：Import/Preflight/Start/Status/Files/Wait/Publish/Pause/Resume/Cancel/Retry/Clean/Unlock/Findings/Document/DecideFinding(s)/AddManual；`Notifier.Subscribe` 供 GUI 推送。每个文件的原件被复制为任务密钥加密的资源，之后所有阶段读该副本；解锁后的副本替代它。

**CLI**（`tacet preflight|run|status|resume|retry|clean`）：显式 `--preset/--authorize/--out`，文件清单 `--list`（`-`为 stdin），`--json` 逐行输出，Ctrl-C 暂停并退出 3；退出码 0/1/2/3 取自 `verdict`；默认使用独立于 GUI 的工作区；平台密钥不可用/工作区被占用返回 1 与封闭码；预检结束后取消并清理，不遗留草稿。

**桌面**：`gui.Service` 暴露 22 个命令/查询（导入、预检、启动、状态、分页文件、发布、暂停/继续/取消/清理、重试、解锁、候选与文档单元分页、批量决定、人工涂抹、原生选文件/文件夹），事件 `tacet:batch-changed`、`tacet:files-dropped`（Wails 原生拖放带真实路径）。前端 `services.engine` 为可选能力：Wails 宿主下 `actions/live.ts` 驱动真实流程（进度来自引擎状态，不用演示计时器），浏览器/测试宿主保持原演示路径。

**本任务中发现并修复的缺陷（均有回归测试，且已用还原修复的变异验证）**
- 两个并行进程的工作进程标记相同，`Terminate` 按标记整机回收时互相误杀（5/5 失败 → 0/6）；
- 删除类文本操作的计划行因 `omitempty` 丢掉必填的 `replacement`，插件拒绝整份计划；旧测试曾固化该缺陷；
- 已确认批次中解锁的文件在“预检完成→加入批次”之间被 `Wait` 视为完成（确定性钩子测试，变异 0.67s 内失败）；
- CLI `--json` 的进度 goroutine 在结果打印时仍可能写输出（`-race` 数据竞争，变异报告竞争）。

**尚未完成（task-10 因此保持未完成）**
- **预览栅格（已做 PDF 页面）**：协议新增 `format.preview`（`PreviewParams/PreviewMark/PreviewRect/PreviewItem`），PDF 插件渲染一页 PNG 并按与回写相同的几何返回候选的像素框，无法定位的标记列为 unmapped；`refs.Grant.ReadBack` 读回瞬时写入；`preview.Pager` 严格校验；`app.PagePreview` 经 `versions` 包装让预览在无阶段运行时通过准入，任何阶段开始或任务变化即失效；`gui.AssetHandler` 提供 `no-store` 的 `/tacet-preview/` 资源；前端 `RasterPage` 以框标出候选、点击可保留/涂抹。**未做**：框选补充涂抹（只有文本视图支持选文）、DOCX 页面预览（无分页）、页内图像处置与 PDF 含图像/批注页的导出（仍按原因码拒绝）、涂抹样式在预览中的呈现（预览按固定样式）；
- 重启恢复已做（`Unfinished`，快捷任务自动回窗口、批次进“最近”）；`lifecycle.Starter/Stopper`、托盘与唤醒事件后来已接入（见下文与 PF-15 证据）；恢复后的文件名为中性名；
- 配置/规则集/预设、模型资源、插件与规则包、设置、更新等页面仍是演示数据，未接 Wails 命令（`config`/`models`/`pluginmgr`/`update` 后端已有）；
- “严格”方式的逐份确认已接通（见 BUX-08 证据：`StartRequest.Mode`、`Engine.ConfirmFile`、`FileStatus.Confirm`、前端“逐份确认”分组）；撤销人工涂抹已做（见 FR-22 证据）；
- 没有在真实桌面窗口里人工操作验证；Windows 仅交叉编译；
- 间歇性：前端整套测试曾出现一次 `window is not defined`（Wails 运行时 `drag.js` 定时器在 jsdom 拆除后触发），重跑 3 次未复现。

**模型接入与命名（task-10 追加）**：引擎在配置了 `Options.Model` 时登记 detect 插件与模型档案，并提供“办公外发（规则 + 模型）”预设（默认）；未找到模型文件时只提供仅规则预设。检测/验证阶段由 `Engine.pipeline` 为快照按需启动模型 worker。发现并修复：输出文件名每批从 f0001 起导致同目录跨批次撞名，现为 `b0001-f0001.ext`。局限：模型文件仍由本地目录提供，未经 `models.Service` 下载/登记；管理页面（预设/规则集/模型/插件/设置/更新）仍为演示数据。

**配置对象接线（task-10 追加）**：内核新增 `Core.Config*`（列出/创建/保存/复制/删除/历史/回滚）与 `config.Repository.Delete`；预设内容增加 `extra_ruleset_ids`（多个规则集合并为一份冻结规则，重复规则编号拒绝），规则内容增加 `name`、`disabled`。前端 `domain/liveConfig.ts`（纯映射）与 `actions/liveConfig.ts`（读取、防抖写回、导入导出、版本）；`known` 弱集合记录与存储一致的屏幕对象，启动后无关改动不会重写它们（变异验证）。已知限制见 FR-19 证据。

**设置与启动诊断（task-10 追加）**：`config.Settings` 增加 `redact_style`、`resource_tier`、`retention_days`；`Core.SettingsGet/SettingsSave`、`gui.Service.Settings/SaveSettings`；前端 `loadSettings/saveSettingsSoon`。worker 启动失败码拆分并把“被停止”与“失败”分开。测试环境：Wails 运行时在导入后用 `window.setInterval` 轮询至多 5 秒，短测试文件结束后 jsdom 已拆除会报 `window is not defined`（约每十余轮一次）；setup 现在在每个文件结束时清除这些定时器，之后连续 33 轮未再出现（概率性结论，不是确定性证明）。

**生命周期、保留与恢复（task-10 追加）**：见 PF-15 与 INV-07 证据。`app.Open` = `lifecycle.Starter`，`Close` = `lifecycle.Stopper`（`archtest` 允许 `app` 依赖 `lifecycle`、`retention`）；`retention` 接入引擎（导入授予、启动清扫、Clean 共用）；`app_member.purged`、`app_run.pending` 两次迁移；Start 先记录请求。**仍未做/未验证**：托盘与睡眠事件、模型经 `models.Service` 下载登记、第三方插件与更新的真实接线（缺沙箱与发布源）、真实桌面窗口的人工验证。

**涂抹样式（task-10 追加）**：见 FR-29 证据。`plan.Look`（dpi + 样式）经 `export.Staged.Look`、`validate.Staged.Look` 进入 `format.write`/`format.validate`；`snapshot.Bindings.Style` 在启动时把预设的 `default` 解析为应用设置。
