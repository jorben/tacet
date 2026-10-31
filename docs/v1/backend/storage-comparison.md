# M0 加密存储比较与资源格式验证

状态：原型证据已建立，生产数据库尚未选型；两平台、R1、模块事务和迁移验收未完成。

## 相同合成负载的候选比较

两者均建 findings 表、`(job,category,id)` 与 `(category,value_hmac)` 索引，单事务写入 100,000 行、200 个任务、10 类，读取筛选前 100 条和分组前 100 条。只使用生成的中性值与专用标记，资源位于权限受限临时目录，结束删除。不把测试标记写法引入生产 Finding schema；生产只存位置/类型/摘要和同值 HMAC。

| 指标 | ncruces SQLite + Adiantum | SQLCipher |
| --- | ---: | ---: |
| 建库与结构 | 0.0022 s | 0.0843 s |
| 十万条写入 | 1.9720 s | 1.3036 s |
| 筛选前 100 条 | 0.000816 s | 0.000551 s |
| 分组前 100 条 | 0.000132 s | 0.000052 s |
| 正确密钥重开行数 | 100,000 | 100,000 |
| 错误密钥拒绝 | 通过 | 通过 |
| 主库/WAL 未出现明文头与标记 | 通过 | 通过 |

命令：

```sh
cd tools/m0-sqlite
go run . --report ../../docs/v1/backend/results/sqlite-adiantum-100k.json
# 在仓库根目录，需先有本机候选库：
python/.venv/bin/python tools/m0_sqlcipher_probe.py \
  --library /opt/homebrew/opt/sqlcipher/lib/libsqlcipher.dylib \
  --report docs/v1/backend/results/sqlite-sqlcipher-100k.json
```

Go 候选在独立 `tools/m0-sqlite/go.mod/go.sum` 锁定，不改变生产模块依赖；SQLCipher 通过本机候选 C API 运行，测试密钥只经 `sqlite3_key` 传入，不放命令行、URI 或报告。主库、WAL、SHM 均扫描标记；SHM 不含该标记不等于其内容加密，SHM 索引元数据保护仍需评审与验证。主库检查点和重开确认后才清理。

比较局限：单次合成开发机实验，不是 R1/R2/Windows 性能验收。Go WASM SQL 驱动与 Python ctypes C API 的绑定开销不同，不能把表中写入差异当成数据库绝对性能排名。Go 堆统计、SQLite 内部分配与进程树 RSS 也不同，不能直接比较内存优劣。

## 尚未冻结的数据库决策

- Adiantum 适合无 cgo 构建，已证实可做有界分页；上游明确仅保密、不提供页级篡改/伪造防护，备份完整性需额外措施。
- SQLCipher 有页级 HMAC，增强数据库完整性；Go 驱动、cgo/OpenSSL、双平台发布构建和署名需要单独落实。
- 在相同目标环境中补齐 Go 集成、WAL/临时/备份加密覆盖、迁移失败回滚、并发配置写、分页索引与发件箱后，再选定；目前没有因本机实验自动修改架构基线。

## Go/Python 分段资源编码

实现采用设计既定 AES-256-GCM、64 KiB 段，格式说明见 `protocol/resource-v1.md`。每次 writer 使用随机 salt 派生资源密钥，段序号/末段标记/资源头绑定 AAD；输出只追加密文。打开先验证末段，随机访问验证相应段，冻结前显式完整 Verify。空资源也有认证末段。

Go 与 Python 均读取同一 `protocol/testdata/resource-v1.json` 向量；该文件只有固定测试密钥与生成标记，不是运行密钥。测试覆盖空、段边界、跨段读取、末段、换段、截断/追加、位翻转、错误密钥/标识、未来版本拒绝、关闭后拒绝、配额失败不能封存，以及同一逻辑资源重写使用新加密上下文。

检查入口：`go test -race ./internal/store ./internal/host ./internal/netgate ./internal/archtest` 与 `cd python && uv run --locked --all-packages python -m unittest discover -s tests`。

局限：这些是 codec 级证据；仍须宿主/SDK 进程互通、真实插件加密读写吞吐、refs 任务授权与 attempt/代次结束后 OS 句柄吊销、沙箱、密钥库与删除、候选冻结摘要以及发布提交的一致性验证。Python 不能保证清零不可变字节及密码库内存；资源关闭不等于已经收回子进程中复制的句柄。
