#!/usr/bin/env python3
"""Regenerate the backend requirement inventory; never infer implementation completion.

The Markdown report is generated from the design baseline and a reviewed evidence
ledger. Evidence entries are deliberately separate from source-code discovery:
a type, interface or passing skeleton test is not an implemented capability.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs/v1"
SOURCES = ("tacet-requirements.md", "tacet-architecture.md", "tacet-module-design.md")
REPORT = DOCS / "backend/requirements.md"
LEDGER = DOCS / "backend/evidence.json"
IDENTIFIER = re.compile(r"\b(?:CORE|INV|FR|BUX|PF|UX|SEC|AR|MD)-\d{2}\b")

# Owning modules and the acceptance method, not a claim about existing code.
GROUPS = (
    ("CORE-01 INV-06 SEC-01 SEC-02 SEC-03 AR-17 MD-19", "netgate, sched, platform, host", "租约并发、出站连接监测、预装后断网真实处理"),
    ("CORE-02 FR-10 FR-18 AR-09", "detect, rules, models, snapshot", "真实 AstrLink + 规则逐类别/语言评测；资源缺失拒绝；质量及映射回归"),
    ("CORE-03 FR-02 FR-24 FR-32 FR-33 AR-02", "preflight, extract, coverage, export, validate", "格式准入、真实文档回写、全部部件去向与残留、Office/WPS 打开验证"),
    ("CORE-04 FR-40 FR-41 FR-42 FR-43 FR-44 FR-45 FR-46 MD-06", "pluginmgr, registry, envs, host, platform", "固定提交/离线安装、确认、环境隔离、引用保护、升级授权及两平台沙箱"),
    ("CORE-05 CORE-06 INV-05 AR-03 AR-04 AR-05 AR-12 AR-14 PF-01 PF-04 PF-05 PF-06 PF-07 PF-08 PF-09 PF-10 PF-11 PF-12 PF-13 PF-17", "sched, host, platform, store, refs", "100/500 MiB、2,000 页、全进程树资源采样、背压、取消、磁盘高水位及 4 h 稳定性"),
    ("CORE-07 BUX-01 BUX-02 BUX-03 BUX-04 BUX-05 BUX-06 BUX-07 BUX-08 BUX-09 BUX-10 BUX-11 BUX-12 BUX-13 BUX-14 BUX-15 BUX-16", "app, jobs, query, review, findings, policy, verdict", "200 份办公文档、10 万候选、跨页/跨文件范围、局部重试与准确清单；UX 用户证据"),
    ("INV-01 FR-06 FR-36 AR-08 MD-05", "query, verdict, report, store", "实际阶段进度；去敏、范围与决定方式分列；发件箱幂等重放"),
    ("INV-02 MD-07", "kernel, jobs, verdict, validate, host", "所有失败/未知枚举/未完成阶段关闭发布门槛，故障注入"),
    ("INV-03 FR-07 FR-19 MD-09", "config, snapshot, registry", "配置版本、冻结内容与分面摘要、并发冲突、历史回滚与不可静默替代"),
    ("INV-04 FR-08 FR-31 AR-07 MD-04", "lineage, jobs, publish, review", "输入与分面变更逐项失效、旧授权拒绝、人工决定重新绑定"),
    ("INV-07 SEC-04 SEC-05 SEC-06", "store, keys, refs, retention, report", "数据库/WAL/备份与分段资源加密；标记扫描；单任务密钥擦除与期限"),
    ("INV-08 SEC-08 SEC-10 SEC-12", "preview, gui, host, platform", "恶意 XML/ZIP/HTML/SVG、无外部资源、选择伪造与桥接隔离"),
    ("INV-09 AR-06", "kernel, extract, plan, preview, validate", "Unicode/字节/UTF-16 与多片段映射；无法可靠关联时阻塞"),
    ("FR-01 FR-03 FR-09", "preflight, gui, cli, platform", "选择/拖放/清单、别名与硬链接、密码不落盘、加密 DOCX/PDF 解锁"),
    ("FR-04 FR-05 MD-08 MD-10", "jobs, workspace, sched, retention", "草稿预检、状态迁移、同事务派发、暂停取消与阶段恢复故障注入"),
    ("FR-11 FR-12 FR-13 FR-14 FR-15 FR-20 FR-26 MD-02 MD-12", "policy, plan, snapshot", "表驱动验证阈值仅影响模型、优先级、重叠冲突、上下文边界与确定性计划"),
    ("FR-17", "rules, detect, policy", "字段路径/表头匹配、精确数字、类型授权和结构不匹配例外"),
    ("FR-21 FR-22 FR-23 MD-11 MD-17 MD-18", "review, exceptions, findings, query", "分组筛选、两阶段批量提交/撤销、范围变更、补充词表与部分结果授权"),
    ("FR-27 FR-29 MD-15", "preview, plan, gui", "有界版本化文本/栅格预览、选区、共用几何、固定纹理与像素核对"),
    ("FR-28 AR-16", "app, jobs, sched, gui", "同类任务并行、等人任务停放、交互优先与批次轮流及一次确认导出"),
    ("FR-30 SEC-11 MD-16", "publish, workspace, platform", "确切字节摘要、授权版本、竞争无覆盖发布与崩溃补记"),
    ("FR-34 FR-35 FR-37 MD-13 MD-14", "validate, coverage, verdict, plan", "再检测与输出关联、部件独立对账、新风险复核、限次扩大/重跑与发布门槛"),
    ("SEC-07 AR-15", "platform, host, refs", "未授权目录/网络/环境写入/逃逸拒绝、句柄继承及代次结束后资源吊销"),
    ("SEC-09 SEC-13", "models, envs, registry", "固定版本清单及全部文件大小/摘要；篡改拒绝与会话失效"),
    ("SEC-14", "pluginmgr, envs, netgate", "仅固定 wheel；拒绝源码构建/安装钩子；中断回滚不损坏已有环境"),
    ("SEC-15 AR-18", "update, lifecycle, store", "签名/摘要拒绝、离线更新、任务处置、迁移回滚及旧版本拒绝"),
    ("AR-01 MD-01 MD-03", "archtest, app, kernel", "导入边界 CI、实体单写者评审、格式与模型隔离"),
    ("AR-10", "app, gui, cli, verdict", "同输入通过真实 GUI/CLI 得到相同逐文件判定和退出码"),
    ("AR-11 PF-14 PF-16", "findings, review, query, store", "10 万候选分页/分组与批量决定计时；不全量加载成员"),
    ("AR-13", "workspace, platform, publish", "多进程/别名工作区锁、旧代次拒绝、取消/清理/发布竞争"),
    ("PF-02 PF-03 PF-15", "gui, query, lifecycle, sched", "R1 界面/冷启动/任务接受实际计时及窗口隐藏处理"),
    ("UX-00 UX-01 UX-02 UX-03 UX-04 UX-05 UX-06", "app, gui, cli, review", "固定任务 + 至少 5 名目标用户，人工时间/主要提交/机器耗时分列"),
)
OWNER = {key: (modules, method) for ids, modules, method in GROUPS for key in ids.split()}
DEFERRED = {"FR-16", "FR-25"}


def inventory() -> tuple[dict, dict]:
    requirements: dict[str, list] = {}
    hashes = {}
    for name in SOURCES:
        content = (DOCS / name).read_text(encoding="utf-8")
        hashes[name] = hashlib.sha256(content.encode()).hexdigest()
        for line, text in enumerate(content.splitlines(), 1):
            for key in set(IDENTIFIER.findall(text)):
                is_definition = bool(re.match(r"(?:\|\s*|[-*]\s*\*?\*?)" + re.escape(key) + r"\b", text))
                requirements.setdefault(key, []).append((name, line, is_definition))
    return requirements, hashes


def render() -> str:
    requirements, hashes = inventory()
    evidence = json.loads(LEDGER.read_text(encoding="utf-8"))
    unknown = set(evidence["requirements"]) - set(requirements)
    if unknown:
        raise ValueError(f"unknown evidence identifiers: {sorted(unknown)}")
    missing = set(requirements) - set(OWNER) - DEFERRED
    if missing:
        raise ValueError(f"missing module/verification mappings: {sorted(missing)}")
    lines = ["# MVP 后端需求追踪", "", "由 `python3 tools/backend_trace.py` 生成；状态只来自经核查的 evidence.json，不从包名、接口或测试是否存在推断完成。", "", "需求原文仍为验收依据，本表不缩减条款。P1 的 FR-16、FR-25 单列范围外；其他编号保持待实现/待验证，直到具有对应实现和实际证据。", "", f"追踪 {len(requirements)} 个不同编号。重复出现及引用不重复计数。需求中重复的 BUX-16 和覆盖说明保留原文；FR-38/39 未定义，不自行新增。", "", "| 编号 | 负责模块 | 验证方法 | 状态 / 证据 |", "| --- | --- | --- | --- |"]
    for key in sorted(requirements):
        occurrences = requirements[key]
        name, line, _ = next((item for item in occurrences if item[2]), occurrences[0])
        link = f"[{key}](../{name}#L{line})"
        if key in DEFERRED:
            lines.append(f"| {link} | — | P1 后续增强 | 本目标范围外 |")
            continue
        modules, method = OWNER[key]
        entry = evidence["requirements"].get(key)
        status = "待实现 / 待验证"
        if entry:
            if entry["status"] not in {"partial", "implemented", "verified", "blocked"}:
                raise ValueError(f"invalid status: {key}")
            if not entry.get("evidence"):
                raise ValueError(f"missing evidence: {key}")
            status = entry["status"] + ": " + entry["evidence"]
        lines.append(f"| {link} | {modules} | {method} | {status} |")
    lines += ["", "## 来源摘要", "", "基线文档变化后重新生成并复核新增/修改要求；摘要变化不代表已完成的新验收。", ""]
    lines += [f"- `{name}`: `{digest}`" for name, digest in hashes.items()]
    lines += ["", "## 模块边界与实现盘点", "", "完整起始盘点见 implementation-plan.md；本表仅列模块边界。实现文件数量与行数不作为完成标准，避免格式化或新文件改变需求追踪内容。", "", "| 模块 | 路径 |", "| --- | --- |"]
    for module in sorted((ROOT / "internal").iterdir()):
        if module.is_dir():
            lines.append(f"| {module.name} | internal/{module.name} |")
    return "\n".join(lines) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail if the checked-in inventory is stale")
    args = parser.parse_args()
    text = render()
    if args.check:
        if not REPORT.exists() or REPORT.read_text(encoding="utf-8") != text:
            raise SystemExit("backend requirement inventory is stale; run python3 tools/backend_trace.py")
    else:
        REPORT.parent.mkdir(parents=True, exist_ok=True)
        REPORT.write_text(text, encoding="utf-8", newline="\n")
    print(f"backend requirement inventory: {'current' if args.check else 'generated'}")


if __name__ == "__main__":
    main()
