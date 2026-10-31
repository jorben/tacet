# M0 首批实现与验证记录

本记录为阶段证据，完整 M0/MVP 尚未完成。需求追踪仍未把任何 P0 标为完整通过。

## DOCX 容器与文本子集

实现：

- `python/sdk/src/tacet_plugin_sdk/safezip.py`：ZIP 中央目录、本地头、描述符和实际条目区间预扫描；重复条目、路径别名、加密、不支持压缩、ZIP64、头不一致、扩张/体积超限等显式拒绝；逐块读取并检测 CRC。ZIP64 当前仅为 M0 子集限制，尚非最终格式边界。
- `python/plugins/format-docx/src/tacet_format_docx/xmlparts.py`：lxml 目标解析器流式处理，只暂存一个受节点与字符预算限制的段落；以 Unicode code point 映射多个 w:t，在原始坐标上从右到左回写，保留原 run 与样式。DTD/实体、处理指令、未映射内容及无策略的隐藏/修订内容拒绝。
- `python/plugins/format-docx/src/tacet_format_docx/package.py`：登记全部 OPC 部件及关系，包括孤立未知条目；受限解析内容类型与关系，禁止外部资源访问。最小文字子集以追加 ZIP 重写并可重新打开；未知部件/外部关系当前阻止原型导出。

依赖：lxml 6.1.3，实际版本与全部 wheel 摘要记录于 `python/uv.lock`；开发环境使用 CPython 3.13.16。这不是已签名可分发资源包。

验证：`cd python && uv run --locked --all-packages python -m unittest discover -s tests`。测试包括 ZIP 歧义、路径穿越、未知条目、CRC、流式描述符、跨 run 中文、emoji、多处长度变化、保留未命中文字与粗体/斜体、XML 预算、DTD、过期/非法定位的拒绝条件。

规模实验：`python/.venv/bin/python tools/m0_docx_probe.py --mib <1|100> --report <结果路径>`，每次为独立进程，只使用生成的固定合成标记，临时目录执行后删除。

| 请求规模 | 实际源文件 | 段落数 | 回写 | 重新登记并抽取核对 | 总计（含生成） | 峰值 RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 MiB | 1,047,357 字节 | 398 | 0.0283 s | 0.0124 s | 0.0430 s | 30,965,760 字节 |
| 100 MiB | 104,856,051 字节 | 39,884 | 2.4391 s | 1.2303 s | 3.7912 s | 31,555,584 字节 |

原始结果见 `results/docx-subset-1mib.json` 与 `results/docx-subset-100mib.json`。两规模峰值差约 0.56 MiB。仅各运行一次，不能给出 P95、重复测量分布或正式吞吐结论。

限制：这是高度重复的合成文字 OPC 子集，不含真实图片、复杂样式/表格/修订、模型、加密资源、宿主 IPC、沙箱或桌面 UI；尚未 Word/WPS 验证。本机 16 GiB M4 不是 R1。尚未实现页码/章节/表格定位、全部部件策略、独立内核对账与发布；不能据此准入 DOCX 或宣称 PF-01 已通过。

## PDFium 栅格链路

实现：`python/plugins/format-pdf/src/tacet_format_pdf/raster.py`。

- 使用 file-like 输入，先调用 PDFium 展平，再重新取得页面句柄，对同一结果逐字符读 Unicode 与字符框；Unicode 映射失败、超页数、超像素、非文字对象、旋转/非零原点等当前显式拒绝。
- 操作区域由调用方提供像素矩形；插件不重新决定敏感区域。生产 Go plan 的共用几何、旋转和覆盖仍待实现。
- `RasterWriter` 按页逐条带压缩 RGB 位图，输出全新的最小 PDF：Catalog、Pages、Page、Image 与 Contents；不复制任何输入对象。仅保留有界页 ID/xref，支持不可 seek 的追加输出。
- 全部异常使候选无效，调用方必须丢弃；模块本身不执行验证授权与公开发布。

初批 5 项测试通过：真实 PDFium 对合成文本 PDF 抽取字符、区域处理并重新打开输出；确认文本提取为空、仅含 Image 对象、无字体/附件、无原文本字节、单一 EOF、采样涂抹像素为黑色；另外验证追加写、输出预算、页数/像素准入及图像源不得作为零文本成功。

限制：仍需注释/表单展平覆盖实证、未知对象完整枚举、图像/转曲策略、回退字体、旋转/坐标变换、全部纹理、独立白名单结构与全区域像素验证、100 MiB/数百页资源实验、目标用户接受度；尚未沙箱或加密链路。M0 子集暂时拒绝 Form 容器，避免 helper 递归深度截断隐藏未登记对象。因此不能据此准入 PDF。

## PDF 注释与表单展平实证

做法：构造 4 种合成 PDF（FreeText 可见注释、Text 便签、Link URI、带值的 AcroForm 文本域）和带 Info 元数据的 PDF，对现有 `rasterize_text_subset` 实测，并直接观察 PDFium 5.14.0 展平前后的页面对象与文本页。

观察到的事实：
- 展平对四种注释都返回成功，并为每种注释新增一个 `FORM` 页面对象，包括没有任何可见内容的 Link 与便签。原先“任何 Form 一律拒绝”的策略因此会让含一个超链接的 PDF 整份被拒，而真实文档里链接很常见。
- FreeText 展平后文字出现在文本页中，带字符框，也被绘制出来；便签、Link 与没有外观流的表单域展平后没有可见内容，文本页里也没有。
- 输出是全新的纯像素 PDF，注释的 `Contents`、URI、表单域值与 Info 元数据都不会进入输出字节。没有外观流的表单域值既不绘制也不抽取，输出中静默缺失（不泄漏，但属于保真缺口，需要在报告里体现）。

策略调整（`raster.py`）：Form 容器仅在遍历确实下探到它时放行；pypdfium2 在 `level >= max_depth - 1` 时不再下探，位于该层的 Form 以 `PDF_FORM_DEPTH_COVERAGE_GAP` 拒绝；Form 内部的对象仍逐个按“只允许文字”判断，含路径或图像的注释（如带背景填充的 FreeText）仍以 `PDF_NON_TEXT_COVERAGE_POLICY_REQUIRED` 拒绝。

验证：`tests/test_pdf_annotations.py` 5 项（便签、Link、表单域放行且输出不含 Contents/URI/域值/`/Annots`/`/AcroForm`，像素与无注释页一致；Info 元数据不进入输出；FreeText 文字可被抽取，字符框区域覆盖其全部深色像素，脱敏后只剩实心区域，容差 1 像素是重新渲染边缘重采样所致；含路径的注释被拒；三层纯文字 Form 放行、20 层嵌套且最深处为路径的 Form 被拒）。3 个变异（去掉深度检查、去掉非文字检查、让 Form 回到旧的类型检查）均被抓住。Python 全量 194 项通过。第一次写测试时用“深色像素数量”为 0 判断脱敏，但黑色填充本身就是深色，是测试写错，已改为包围盒判断，实现未动。

局限：仅合成的单页 PDF 与 PDFium 5.14.0；没有真实办公 PDF、签名域、XFA、JavaScript、portfolio/附件、带背景的批注、旋转/CropBox 页面、加密 PDF；表单域值静默缺失尚未进入覆盖报告；不是 PDF 准入。

## DOCX 表格与版面词表增量

`xmlparts.py` 的子集词表在 `document/body/p/r/rPr/b/i/t` 之外增加了不含文字的版面元素：`tbl`、`tr`、`tc`、`tblPr`、`tblGrid`、`tcPr`、`pPr`、`sectPr`，以及带属性的 `tblW`/`tcW`（`w` 为至多 9 位数字，`type` 取 dxa/auto/pct/nil）、`gridCol`、`jc`（固定枚举）、`pgSz`、`pgMar`（数字）。属性名与取值不在表内一律以 `DOCX_TEXT_SUBSET_UNSUPPORTED` 拒绝；版面元素内的文字、未知子元素（如 `tblStyle`、`headerReference`）、嵌套段落仍拒绝；单元格内的隐藏/修订段落仍须策略。表格内段落与正文段落走同一条逐段编辑路径。

验证：`tests/test_docx_xml.py` 新增 5 项（含表格单元格改写后版面元素与属性原样保留、属性格式非法、版面元素内文字与未知子元素拒绝、单元格内隐藏文字拒绝、嵌套段落拒绝）；3 个变异（未知属性放行、数字检查去掉、枚举检查去掉）均被抓住。Python 全量 199 项通过。

尝试用本机 Word 的 AppleScript 生成真实 DOCX 三次：前两次是脚本语法错误（页眉接口参数名为 `index`），第三次通过编译检查后在 Word 内挂起约 2 分钟、未产生文件，被 120 秒硬超时终止，原因未查明（疑似 Word 等待界面确认或文件访问授权，未验证）。因此仍没有 Word 生成的真实样本。

局限：仍为合成样本，没有 Word 生成的真实表格（合并单元格、嵌套表格、样式引用）；`pStyle`、`tblStyle`、页眉页脚引用、批注、修订、图片、超链接、域、书签、`styles.xml`/`numbering.xml` 等仍使导出整体被拒或登记为缺口；没有用 Word 或 WPS 打开含表格的候选；不是 DOCX 准入。

## 模型与沙箱增量证据

模型候选已固定 `QuantumNous/astrlink-guard` v0.1.3 对应提交 `7216b6a3b897ef903d4b1ba367ae6fb95b0fa5e4`，运行文件的大小与摘要见 `resources/models/astrlink-guard-int8.json`，资源只保存于已忽略的 `.pi/m0-resources/`。完整校验后实际 CPU 加载成功，14,021 字符合成文本分成 16 个窗口并覆盖末尾；ONNX Runtime 1.31.1，tokenizers 0.23.3，2 个推理线程，加载约 1.139 s，峰值 RSS 174,702,592 字节。原始结果见 `results/astrlink-loading.json`。这是加载/分窗/输出形状验证，未实现官方 BIO+IP/URL 解码，不是质量评测或校准；Tacet 校准阈值仍为空，无人值守资格关闭。

macOS Seatbelt 生成数据原型通过未授权读取、环境写入、loopback 连接及 fork 拒绝；继承 fd3 可读，lxml/PDFium/ONNX Runtime/tokenizers 可导入。根目录的精确 `file-read* literal` 是本机运行时启动必需规则，不允许访问任意根目录后代。原始结果见 `results/macos-seatbelt.json`，脚本为 `tools/m0_sandbox_probe.py`。尚未签名 App Sandbox helper 比较、完整工作负载、Windows AppContainer、进程树/孤儿与资源控制验收，不能开放第三方插件。

## AstrLink 分窗与解码增量证据

`python/plugins/detect-astrlink/src/tacet_detect_astrlink/{decode,detector}.py` 实现 Tacet 自有的 512/128 分窗、BIO 解码（不连续的 I 标签或标签变化开新片段，片段分数取其词元最小概率）、字符偏移校验与重叠窗口同标签合并（取最高分）。模型输出形状、非有限数、偏移越界均 fail-closed。适配器下限暂设 0.5，Tacet 方案阈值在处置阶段应用，不在检测插件内过滤。

`python/tests/test_astrlink_decode.py` 含 5 项纯逻辑测试，以及固定提交真实模型的 4 项冒烟测试（资源缺失则跳过）：模型卡示例中的人名、手机号、邮箱按 code point 精确定位；emoji 前缀不偏移；日志时间戳无片段；约 1.4 万字符长文末尾手机号跨窗口只出现一次。

**更正（与上游参考解码的差分比对）**：上面描述的"不连续的 I 标签或标签变化开新片段"之外，初版还加了"词元偏移与前一片段重叠则开新片段"的自加规则。对固定提交模型卡中的参考解码片段做差分比对（`test_matches_model_card_reference_decoder_on_real_output`，真实 CPU 输出）时发现，SentencePiece 相邻词元的偏移可相差一个字符而重叠，该规则会把完整片段切碎：`1992 年 7 月 8 日` 被切成四段互相重叠的 private_date，地址末尾多出单独的 `号`。此前的冒烟测试没有覆盖，说明它们不足以证明解码正确。该规则已删除，重叠时片段末端取较大值；新增合成重叠用例和差分测试，在旧代码上均失败，修复后通过（Python 全量 109 项）。现在 6 条模型卡示例文本的片段与参考解码一致，模型卡公布的片段（人名、日期、地址、手机、邮箱、IP、URL、密钥）也全部命中。

局限：模型卡只给出契约名称，没有公布 `bio-offset-consistency-v1`（偏移一致性）与 `existing-ip-and-dynamic-url-v1`（IP/URL 起止清理）的算法；**本实现只与模型卡参考片段一致，没有实现 IP/URL 边界清理，不声称符合这两个契约**，参考片段的朴素 argmax 解码本身也被模型卡说明"可能略有差异"。没有逐类别 P/R、校准、中文办公语料评测与规则补充，不能授予任何无人值守资格。另外观察到模型对 `QQ 12345678` 只给 0.37 的 account 分数（低于 0.8 建议阈值），属于逐类别评测要关注的点。M0 第 3 项仍未完成。

## 规则引擎增量证据

`internal/rules` 实现声明式规则的编译与匹配：RE2 语法；含前后断言、反向引用、`^ $ \b \B` 与空匹配的规则在加载时拒绝并给出稳定错误码；边界属性 `digit/word/cjk` 在匹配后检查相邻字符，被拒绝时从该匹配起点的下一个字符重试，语义与对应前后断言一致；字典词条按最长匹配且可忽略大小写；校验器仅限已注册的 Luhn 与身份证校验位（含出生日期与闰日），“疑似”规则保留校验失败命中并标记 `ValidatorMismatch`。编译预算覆盖规则数、模式长度、词表体积、词条长度和编译后指令数；匹配预算按实际扫描量计费，命中数与单元大小设上限，超限或取消返回错误而不是部分命中。命中位置为 code point，通过单一游标线性换算。

测试：`go test -race ./internal/rules`（12 项）。已验证 50,000 个命中的偏移换算为线性，`\d+` 加 `word` 边界在 `111…1a` 上的二次方重试被预算截断。局限：词表为单一正则交替，尚未做多模式自动机；规则预算数值是初始值，未按 R1 档实测冻结；未接入 detect 流水线与 Python 检测结果合并；中国场景规则集尚未编写。

## Word 打开验证（单一构建）

`tools/m0_word_probe.py` 生成固定的合成 DOCX 子集，经 `rewrite_text_subset` 回写后，用 Microsoft Word 16.114.1（macOS，本机）以 `repair false`、只读方式打开并读回正文，关闭且不保存。原始样本与回写候选均打开成功，候选正文为 `联系人[人名]结束`，与预期一致；末尾截断 30 字节的负对照被 Word 拒绝，证明未启用自动修复。原始结果见 `results/word-open.json`。

局限：仅一个 Word 构建、仅生成的纯文字子集（无样式、表格、图片、页眉页脚、修订、批注）；没有 WPS、没有 Windows 版 Word、没有真实办公文档；“无修复提示”仅按 AppleScript 不报错且不阻塞判断，未做人工目视确认。因此这是需求 6.3 节 Word 项的**部分**证据，不构成 DOCX 准入。

## 可重定位离线运行时（macOS，本机）

`tools/m0_bundle_probe.py`：构建阶段复制 uv 受管 CPython，用 `uv export --locked` 带哈希的 requirements 以 `--require-hashes --no-build` 安装第三方 wheel，工作区包先 `uv build --wheel` 再非可编辑安装（SDK 的 Schema 随 wheel 打包）；运行阶段把整棵目录移到含空格的新路径，清空环境（仅 `PATH=/usr/bin:/bin`），在禁网 Seatbelt 下导入 lxml、PDFium、Pillow、ONNX Runtime、tokenizers、numpy、cryptography，完成一次 PDFium 渲染冒烟，并启动真实 `tacet_format_docx` 插件完成协议握手。`otool -L` 逐个检查 .so/.dylib 的依赖，只允许系统目录；遇到无法解析的输出会报错而不是跳过。结果 5 项全部通过，见 `results/relocatable-runtime.json`；运行时约 298 MB，运行目录不含 uv 或 `.pth` 可编辑引用。

局限：同一台机器与用户，不是干净系统镜像；未签名、未启用 hardened runtime、未公证；无 Windows；仅 Seatbelt，无硬资源限制；AstrLink 权重是单独下载资源；298 MB 的体积需要纳入安装包与磁盘预算评估。这是架构 7.1、12.3 离线环境条目的部分证据。

## 进程监督增量证据

`internal/platform/proctree*.go` 提供进程树回收原语：macOS 使用独立进程组并向负 pid 发送 SIGKILL；Windows 使用 Job Object（KILL_ON_JOB_CLOSE），仅完成交叉编译和 vet，未在 Windows 实机执行。`internal/host/supervisor.go` 以绝对路径、结构化参数和完整环境白名单启动 worker，stderr 只计数不存储；`Terminate(grace)` 先关闭输入、等待宽限期，再回收整棵树，可重复调用。三项真实 Python 子进程测试通过：忽略取消并带有子进程的 worker 在 700 ms 截止后被回收，子进程确认退出，总耗时不足 5 s；宿主环境变量未继承；启动即崩溃的 worker 使握手立即失败。

**setsid 逃逸与代次标记回收**：真实复现了 macOS 上 `setsid` 后代逃出进程组（`Kill` 之后仍存活）。补充机制：`platform.Mark` 给 worker 注入 `TACET_TREE=<代次标记>`（8–64 位字母数字、`_`、`-`），后代继承；`platform.ReapMarked` 经 `kern.proc.uid` 与 `kern.procargs2` 读取同用户进程的环境，只匹配环境区的精确条目（命令行参数里出现同样字符串不算）并 SIGKILL，排除自身。`host.Spec.Generation` 非空时，`Terminate` 在组回收后立即按标记回收。同一函数也可用于启动时回收上一代次遗留进程（尚未接入启动流程）。Windows 的 `ReapMarked` 为空实现，依赖 Job Object 的 KILL_ON_JOB_CLOSE，仅编译通过，待实机验证。

测试：platform 8 项真实进程测试（`setsid` 后代逃过 `Kill`、被标记回收；仅回收指定标记，不误杀其他标记或无标记进程；非法标记拒绝；`Mark` 去重且 nil 环境仍继承；环境区解析；不向自身发信号），host 增 3 项（带标记的 Terminate 回收 `setsid` 后代；无标记时后代存活，记录局限；非法标记拒绝）。变异验证：环境匹配改前缀匹配、把参数当环境、自排除去掉、不发信号、监督器用错标记、去掉 `WaitDelay`，均被抓住。

排查中发现的问题：
- **监督器会永久挂起（真实缺陷，已修）**：`cmd.Stderr` 不是文件，`exec` 起拷贝 goroutine，`cmd.Wait` 要等它结束；逃出进程组的后代继承了 stderr 管道，主进程退出后 `Wait` 不返回，`Terminate` 卡在 `<-w.done`。修复：`Kill` 后立即按标记回收，再等 `done`；并设 `cmd.WaitDelay = 1s`，使无标记时也有上限（此前无标记并有逃逸后代则 `Terminate` 永久阻塞）。
- **内核对系统二进制隐藏环境**：macOS 27.2 上，`kern.procargs2` 对受 SIP 保护的系统二进制（如 `/bin/sleep`）只返回参数，不返回环境；对受管 CPython 等普通二进制可读。因此测试的休眠进程改为测试程序自身和受管解释器。`ps -E`、`ps eww` 在此系统读不到任何进程的环境，不可用。结论仅基于本机一个系统版本，未在其他 macOS 版本、签名并启用 hardened runtime 的发布版解释器上验证。
- **测试自身的泄漏**：一个测试的清理只调用了够不着 `setsid` 后代的 `tree.Kill`，每次运行遗留进程；另有调试和变异运行遗留的 3 个逃逸进程。已改为按 pid 清理并手动清掉。我清理时用的 `pkill -f "time.sleep(300)"` 因括号被当成正则分组而没有生效，后来才发现。

局限：标记是恢复手段，不是安全边界——清空自身环境的后代能躲过；内存上限监控、Windows 挂起启动以消除分配前窗口均未实现；此机制本身不是沙箱。

## 存储与平台增量证据

两个数据库候选均运行了实际十万条写入/分页/分组、主库及 WAL 标记扫描、错误密钥拒绝和重新打开行数检查，详见 storage-comparison.md。数据库生产选型仍未冻结。

Go/Python 资源 codec 共享固定向量，且真实独立 Go 进程与 Python 进程双向读取 300,010 字节多段资源一致。Python 直接在加密资源流上进行 DOCX ZIP 读取和追加回写并重新验证；本次暴露并修复了 ResourceReader 缺少 seekable 的真实兼容性问题。原始结果见 `results/resource-process-interop.json`。原型通过测试路径管理资源，生产仍须句柄传递/吊销，不能把互通等同授权完成。

macOS OS 文件锁在独立进程竞争、硬链接别名和关闭自动释放测试中通过。Windows LockFileEx 实现及测试已交叉编译，尚无 Windows 执行证据。Go host 帧编解码与网络/处理/维护租约已具备行为测试；前者还需角色 Schema 与完整会话，后者还需真实出站客户端集成。

## JSON / JSONL 流式扫描器

实现：`python/plugins/format-json/src/tacet_format_json/scanner.py`。严格 RFC 8259 字节级扫描（拒绝 NaN/Infinity、前导零、尾随逗号、控制字符、非法 UTF-8、孤立代理项），按事件返回绝对字节区间：数值只给原词法（`1.00`、`1E400`、`-0`、任意长整数不经浮点）；重复键不合并，带同一对象内出现序号；字符串分块返回，每个码点有到原始字节的偏移，转义和代理对也对得上；`rewrite` 只替换被编辑的字节区间，其余字节原样复制，替换文本重新转义；JSONL 按行记录，记录不得跨行、同行不得有多个值。深度、键长/键数、数字长度、字符串总长和单记录字节数均有上限，超限抛 `JsonError`（稳定错误码与偏移，不含内容）。未选用第三方库：常见流式库把数值转成 Decimal/float，丢失原词法，也不给转义字符的原始区间。

验证：`cd python && uv run --locked --all-packages python -m unittest tests.test_json_scanner`（35 项）：语法接受/拒绝表并与标准库对照；300 个随机文档在读块 1/5/262144 字节下重建后与 `json.loads` 一致；读块 1–64 字节的事件流差分；转义/emoji 偏移；跨块边界的回写；20 MiB 流式数组峰值低于 8 MiB。变异验证：去掉转义偏移记录、去掉数字前瞻、让重复键计数失效，均被测试抓住。写测试时发现并修复数字词法在缓冲区边界被截断的缺陷。

规模实验：`python/.venv/bin/python tools/m0_json_probe.py --shape <array|jsonl|string> --mib 100 --report <路径>`，每形状一个独立进程，依次扫描、回写（替换全部合成标记，含跨块标记）、再扫描核对；标记与数值为生成的固定合成内容。

| 形状 | 实际源文件 | 扫描 | 回写 | 重新扫描核对 | 峰值 RSS | 残留标记 | 替换 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 数组 | 104,857,481 字节 | 21.134 s | 21.464 s | 19.438 s | 24.2 MiB | 0 | 806,596 |
| JSONL | 104,857,584 字节 | 20.229 s | 21.546 s | 18.388 s | 24.4 MiB | 0 | 1,344,328 |
| 单个长字符串 | 104,857,273 字节 | 4.27 s | 4.492 s | 4.273 s | 29.2 MiB | 0 | 80,846 |

原始结果见 `results/json-{array,jsonl,string}-100mib.json`。每形状仅运行一次，不是分布。

一次实测发现的问题：最初默认读块 256 KiB、分块 65536 码点时，长字符串峰值 RSS 随输入线性增长（100 MiB 时约 234 MB，同一文件重复扫描每轮继续上升），tracemalloc 显示 Python 存活对象为零，调小读块与分块后增长消失，判断为大块缓冲在 macOS 分配器中的保留，并非对象泄漏（未用其他分配器或平台复现）。默认值改为读块 64 KiB、分块 16384 码点，之后 80/160 MiB 与连续三轮扫描峰值均稳定在约 27 MB。分块后标记可能跨块，探针和宿主都必须处理跨块匹配，这也是协议需要重叠分块的原因。

局限：合成、高度重复的数据，不含真实业务 JSON；只有扫描与回写原语，没有 `format.*` 协议处理、字段路径/表头策略、改键与类型授权的冲突检查，也没有 CSV/TXT/XLSX；纯 Python，约 5 MiB/s 的数组吞吐在 R1 上未测，数百 MiB 日志的耗时需另行评估；开发机非 R1，无 Windows。这不是 JSON/JSONL 格式准入，也不能据此宣称 FR-17 通过。

## TXT / Markdown / 日志与 CSV 流式扫描器

共用原语：`python/sdk/src/tacet_plugin_sdk/byteedit.py`（`Edit`、`FormatError`、`rewrite`）：按排序且不重叠的字节区间编辑复制源文件，其余字节原样保留；JSON、文本、CSV 三个插件共用，各自把错误转成带稳定错误码与字节偏移、不含内容的子类（JSON 重构后 35 项测试仍通过）。

**文本**（`format-text/.../scanner.py`）：按声明编码增量解码，白名单为 utf-8/utf-8-sig、utf-16-le/be、gb18030、big5、cp1252；不翻译换行，每块给出每个码点的绝对字节偏移与起始行号（CRLF 跨块按一个换行计）。每段解码后重新编码，必须与来源字节完全一致，否则以 `non_canonical_encoding` 拒绝（big5 存在 4 组真实不可往返字节对，已用于测试；gb18030 在 2 字节全空间与部分 4 字节范围内未找到）。非法字节、UTF-16 与另一字节序 BOM、不支持编码（含 ISO-2022、utf-7、shift_jis）均 fail-closed。19 项测试，含读块 1–65536 与分块 1–16384 差分、跨块 CRLF 行号、随机文件；4 个变异（去往返校验、忽略 BOM 偏移、跨块 CRLF 重复计数、忽略解码器缓存字节）均被抓住。

**CSV**（`format-csv/.../scanner.py`）：**偏离架构 7 节"使用标准库 csv 流式读写"的表述**。标准库不给出单元格字节位置，经 `csv.writer` 回写会改变未命中单元格的引号与换行，不满足"未命中范围保持一致"，所以自写字节级扫描器，并以 `csv.reader(strict=True)` 做差分验证（300 个随机表格、读块 1–65536 字节，行与单元格一致）。语法为 RFC 4180 加单字节分隔符；非引号单元格内的引号、闭合引号后的文字、未闭合引号均拒绝（比标准库默认方言更严）；空行是零单元格记录；行宽不齐只上报。回写只替换被编辑单元格，整格重写，原本带引号保持带引号，需要时才加引号；公式注入防护：新值首字符为 `= + - @ TAB CR` 且与原值首字符不同时前缀单引号（可显式关闭）。仅限分隔符、引号、CR/LF 字节不会出现在多字节序列内的编码（UTF-8、GB18030、Big5、cp1252）；UTF-16 暂不接受，需宿主转码。28 项测试；首轮 7 个变异中 6 个被抓住，第 7 个（引号单元格提前越界检查）存活。

**一次实测发现的缺陷**：为补上述存活变异写了"无限数据源"用例，非引号单元格读取循环在越界检查之前一直读取，缓冲区无限增长（每次还整体拷贝），`max_cell_bytes` 形同虚设；该测试因未加超时而卡死，需人工中断。修复：循环内先检查已读长度再继续读。带 15 秒硬超时的堆栈定位到该循环，修复后通过，去掉检查再跑则被超时打断。此后自行构造的、可能不收敛的脚本一律加超时或放后台。

规模实验：`python/.venv/bin/python tools/m0_text_probe.py --shape <text|longline|csv> --mib 100 --report <路径>`，每形状独立进程，自带 25 分钟 faulthandler 硬超时，依次扫描、回写（替换全部合成标记，含跨块标记；CSV 为含标记单元格整格改写）、重新扫描核对。

| 形状 | 实际源文件 | 扫描 | 回写 | 重新扫描核对 | 峰值 RSS | 残留标记 | 替换 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 日志文本（CRLF，多行） | 104,857,564 字节 | 3.716 s | 4.817 s | 3.208 s | 30.9 MiB | 0 | 1,542,023 |
| 单行无换行文本 | 104,857,262 字节 | 3.597 s | 3.774 s | 3.607 s | 29.9 MiB | 0 | 80,846 |
| CSV（2,097,153 记录） | 104,857,614 字节 | 5.035 s | 7.109 s | 5.293 s | 24.4 MiB | 0 | 2,097,152 |

原始结果见 `results/text-{text,longline,csv}-100mib.json`，每形状仅运行一次，不是分布。

局限：合成、高度重复数据；仅有扫描与回写原语，无 `format.*` 协议处理、字段/列策略、编码自动探测（编码由调用方声明）、CSV 表头上下文与类型影响说明；XLSX 未做；开发机非 R1，无 Windows。不是 TXT/CSV 格式准入，也不能宣称 FR-17 的 CSV 列部分通过。

## XLSX 值子集原型

共用：`python/sdk/src/tacet_plugin_sdk/opc.py`（OPC 部件与关系登记，错误码前缀、主部件类型、角色分类由参数传入）；DOCX 改为薄封装，原有测试未改动。XLSX 插件：`xmlunits.py`（SAX 目标，一次只缓冲一个 `c` 或 `si` 单元，其余按白名单元素流式写出；注释、处理指令、DTD、单元外文字一律拒绝）、`cells.py`（单元格与共享字符串模型）、`workbook.py`（部件门禁与回写）。

子集：工作簿、工作表、至多一个共享字符串部件，样式与主题原样复制。所有工作表（含 hidden、veryHidden）都处理并上报状态。被改动的共享字符串单元格转为内联字符串；失去全部引用的共享字符串条目被清空（位置保留，索引仍有效），`count` 更新；对共享字符串可按唯一条目一次编辑，保留 `rPr` 等格式节点。数值或日期单元格改成文本须调用方显式 `allow_type_change`。以下情形整体拒绝导出：公式（含未改动的）、拼音注释、`_xHHHH_` 转义、未知单元格属性或子节点、定义名称/外部引用/透视缓存等工作簿构造、批注/绘图/属性等未检视部件、外部关系、无工作簿条目指向的工作表部件、共享索引越界。

验证：`tests/test_xlsx_value_subset.py` 27 项；9 个变异（公式门禁、孤儿条目清空、孤儿工作表检查、外部关系检查、`count` 更新、共享索引边界、类型改变授权、拼音拒绝、转义拒绝）均被抓住。写测试时发现并修复两处：增量写出器把 `xml:space` 写成 `ns0:space` 并为 xml 命名空间绑定其他前缀（违反 Namespaces in XML，Excel 可能拒收），含该属性的单元改为整体写出；每个 `c` 重复声明 `xmlns`，改为沿用外层声明。Python 全量 183 项通过。

规模实验：`python/.venv/bin/python tools/m0_xlsx_probe.py --mib 100 --unique 200000 --report <路径>`，单进程、25 分钟硬超时；合成工作簿，工作表约 100 MiB 未压缩（压缩后文件 15,393,778 字节，499,120 行、1,996,480 单元格、200,000 个唯一共享字符串）。

| 指标 | 结果 |
| --- | --- |
| 回写（含预扫描与全部部件写出） | 39.656 s |
| 重新扫描核对 | 10.077 s |
| 峰值 RSS | 30.6 MiB |
| 改动 | 共享字符串 66,667，内联单元格 99,824（合计 166,491 与核对到的替换数一致） |
| 残留标记 | 0 |

原始结果见 `results/xlsx-100mib.json`，仅运行一次，不是分布。

局限：默认下 `docProps` 等属性部件会使导出整体被拒；调用方显式传 `strip_properties=True` 才会删除由包根关系指向的 core / extended / custom 属性与缩略图部件，并同步删除 `[Content_Types].xml` 的 Override 与 `_rels/.rels` 的关系（删除属于对用户文件的改动，须由策略授权并写入报告 `parts_removed`）。被其他关系同时引用的属性部件、没有根关系的孤立属性部件、以及批注/绘图/表格等其他未检视部件仍然整体拒绝。该选项 6 项测试（含 5 个变异验证：共享引用检查、根关系清理、内容类型清理、门禁豁免、仍写出被删部件）。仍未设计的是批注、超链接、绘图等部件的处置，因此含这些内容的真实 Excel 文件仍通不过；合成数据；没有 Excel/WPS 打开验证；未处理公式及缓存、批注、超链接、隐藏行列标注、条件格式等；`safezip` 暂不接受 ZIP64；预扫描使回写读取工作表两次；开发机非 R1，无 Windows。这不是 XLSX 准入，不能据此宣称 FR 中表格交付通过。

## 尚缺的 M0 关口

模型官方解码与自有逐类别质量/校准、两平台完整沙箱与进程树、离线签名解释器、数据库 Go 集成与备份迁移、独立内核覆盖/发布，以及真实 Office/WPS、R1/R2、用户验收。具体条件与规则见 implementation-plan.md 与 m0-decisions.md。
