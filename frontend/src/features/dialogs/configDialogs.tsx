import { useRef, useState } from "react";
import {
  deleteRule, finishImport, importNameClashes, rollback, saveRule, versionTarget, type RuleDraft,
} from "../../actions/presets";
import { CATEGORIES, CATEGORY_IDS } from "../../domain/catalog";
import type { Boundary, RuleKind } from "../../domain/types";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Field, List, Segmented, Swatch, Tag, TextArea, pushLeft } from "../../ui";

const BOUNDARY_OPTIONS: { value: Boundary; label: string }[] = [
  { value: "", label: "不限" }, { value: "digit", label: "不紧邻数字" }, { value: "word", label: "不紧邻字母或数字" },
];

/** Add or edit a rule: a word list or a regular expression, what it counts as, and (for expressions) its boundaries. */
export function RuleEditorDialog({ rulesetId, index }: { rulesetId: string; index: number | null }) {
  const existing = useAppState((s) => s.rulesets.find((r) => r.id === rulesetId)?.rules[index ?? -1]);
  const [draft, setDraft] = useState<RuleDraft>(() => ({
    name: existing?.name ?? "", kind: existing?.kind ?? "词表", cat: existing?.cat ?? "other", bd: existing?.bd ?? "", body: existing?.body ?? "",
  }));
  const nameRef = useRef<HTMLInputElement>(null);
  const bodyRef = useRef<HTMLTextAreaElement>(null);
  const set = <K extends keyof RuleDraft>(k: K, v: RuleDraft[K]) => setDraft((d) => ({ ...d, [k]: v }));
  const save = () => {
    const result = saveRule(index, draft);
    if (result === "name") nameRef.current?.focus();
    else if (result === "body" || result === "lookaround") bodyRef.current?.focus();
  };
  const regex = draft.kind === "正则";
  return (
    <Modal>
      <ModalHeader title={index != null ? "编辑规则" : "添加规则"} />
      <ModalBody>
        <Field ref={nameRef} id="rdName" placeholder="名称" aria-label="名称" value={draft.name} onChange={(e) => set("name", e.target.value)} />
        <Segmented full style={{ marginTop: 12 }} value={draft.kind} onChange={(k: RuleKind) => set("kind", k)} options={[{ value: "词表", label: "词表" }, { value: "正则", label: "正则" }]} />
        <TextArea
          ref={bodyRef}
          id="rdBody"
          placeholder={draft.kind === "词表" ? "每行一个词条" : "正则表达式"}
          aria-label="内容"
          className={regex ? "mono" : undefined}
          value={draft.body}
          onChange={(e) => set("body", e.target.value)}
        />
        {regex && (
          <>
            <div className="mh" style={{ padding: "16px 0 6px" }}>
              前后边界
            </div>
            <Segmented full value={draft.bd} onChange={(v) => set("bd", v)} options={BOUNDARY_OPTIONS} />
          </>
        )}
        <div className="mh" style={{ padding: "16px 0 6px" }}>
          命中归类
        </div>
        <div className="typegrid" style={{ padding: 0 }}>
          {CATEGORY_IDS.map((t) => (
            <button key={t} type="button" aria-pressed={draft.cat === t} onClick={() => set("cat", t)}>
              <Swatch category={t} />
              {CATEGORIES[t].l}
            </button>
          ))}
        </div>
      </ModalBody>
      <ModalFooter>
        {index != null && (
          <Button variant="danger" style={pushLeft} onClick={() => deleteRule(index)}>
            删除
          </Button>
        )}
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" onClick={save}>
          保存
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** The last versions of a scheme or rule set, each with a rollback. */
export function VersionsDialog({ kind }: { kind: "preset" | "ruleset" }) {
  const target = useAppState((s) => versionTarget(s, kind));
  const history = (target.hist || []) as { ver: string; when: string }[];
  return (
    <Modal>
      <ModalHeader title="历史版本" subtitle={target.name + " · 保留最近 5 个版本"} />
      <ModalBody>
        <List>
          <div className="row">
            <span className="rl">
              <Tag primary>{target.ver}</Tag>
            </span>
            <span className="rv">
              <span className="muted sm">{target.edited || ""} · 当前</span>
            </span>
          </div>
          {history.map((h, i) => (
            <div className="row" key={i}>
              <span className="rl">
                <Tag>{h.ver}</Tag>
              </span>
              <span className="rv">
                <span className="muted sm">{h.when}</span>
                <Button onClick={() => rollback(kind, i)}>回滚</Button>
              </span>
            </div>
          ))}
        </List>
        <div className="muted sm" style={{ marginTop: 10 }}>
          回滚会以所选版本的内容保存为新版本，其他版本保持不变
        </div>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          关闭
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** An import whose name is taken: the user picks another one; nothing is overwritten or merged. */
export function ImportConflictDialog({ kind, name }: { kind: "preset" | "ruleset"; name: string }) {
  const [value, setValue] = useState(name + " (导入)");
  const [error, setError] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const submit = () => {
    const n = value.trim();
    if (!n) { input.current?.focus(); return; }
    if (importNameClashes(kind, n)) { setError("仍与已有名称重复"); input.current?.focus(); return; }
    finishImport(kind, n);
  };
  return (
    <Modal>
      <ModalHeader title="名称冲突" subtitle={`已有同名${kind === "ruleset" ? "规则集" : "方案"}「${name}」，导入不会覆盖或合并`} />
      <ModalBody>
        <Field ref={input} id="impName" aria-label="新名称" value={value} onChange={(e) => setValue(e.target.value)} />
        <div className="sm" id="impErr" style={{ marginTop: 8, color: "var(--bad)" }}>
          {error}
        </div>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消导入
        </Button>
        <Button variant="primary" onClick={submit}>
          改名导入
        </Button>
      </ModalFooter>
    </Modal>
  );
}
