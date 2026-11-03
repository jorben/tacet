import { useRef, useState } from "react";
import {
  deleteRule, finishImport, importNameClashes, rollback, saveRule, versionTarget, type RuleDraft,
} from "../../actions/presets";
import { createCategory, defaultPlaceholder } from "../../actions/categories";
import { categoryName, categoryOf } from "../../domain/categories";
import type { Boundary, CategoryId, RuleKind } from "../../domain/types";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal, openModal, type ModalSpec } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Field, Icon, List, Segmented, Swatch, Tag, TextArea, pushLeft } from "../../ui";

const CREATE_ERRORS = { name: "请填写类别名称（至多 32 个字）", placeholder: "占位符至多 64 个字，且不能换行", taken: "已有同名类别", failed: "未能创建，请重试" } as const;

const BOUNDARY_OPTIONS: { value: Boundary; label: string }[] = [
  { value: "", label: "不限" }, { value: "digit", label: "不紧邻数字" }, { value: "word", label: "不紧邻字母或数字" },
];

/**
 * Add or edit a rule: a word list or a regular expression, what it counts as, and (for expressions) its boundaries.
 * `index` is the rule's index in the rule set. A new rule reports `category` until another is picked ("other"
 * without one); an existing rule shows its own category. `initial` is what the editor held when it was left.
 */
export function RuleEditorDialog({ rulesetId, index, category, initial }: { rulesetId: string; index: number | null; category?: CategoryId; initial?: RuleDraft }) {
  const existing = useAppState((s) => s.rulesets.find((r) => r.id === rulesetId)?.rules[index ?? -1]);
  const [draft, setDraft] = useState<RuleDraft>(() => initial ?? ({
    name: existing?.name ?? "", kind: existing?.kind ?? "词表", cat: existing?.cat ?? category ?? "other", bd: existing?.bd ?? "", body: existing?.body ?? "",
  }));
  // every category of the catalog can be reported by a rule; a stored category the catalog lacks stays selectable
  const catalog = useAppState((s) => s.categories);
  const choices = catalog.some((c) => c.id === draft.cat) ? catalog : [...catalog, categoryOf(catalog, draft.cat)];
  // a category the catalog lacks is made here, while writing the rule that reports it
  const [creating, setCreating] = useState<{ name: string; placeholder: string; error?: string } | null>(null);
  const create = async () => {
    if (!creating) return;
    const got = await createCategory(creating.name, creating.placeholder.trim() || defaultPlaceholder(creating.name));
    if ("id" in got) {
      set("cat", got.id);
      setCreating(null);
    } else setCreating({ ...creating, error: CREATE_ERRORS[got.error] });
  };
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
          {choices.map((c) => (
            <button key={c.id} type="button" aria-pressed={draft.cat === c.id} onClick={() => set("cat", c.id)}>
              <Swatch category={c.id} />
              {c.name}
            </button>
          ))}
          {!creating && (
            <button type="button" className="typegrid-add" onClick={() => setCreating({ name: "", placeholder: "" })}>
              <Icon name="plus" size={12} />
              新建类别
            </button>
          )}
        </div>
        {creating && (
          <div className="newcat" role="group" aria-label="新建类别">
            <Field id="rdCatName" placeholder="类别名称，如 项目代号" aria-label="类别名称" maxLength={32} value={creating.name} onChange={(e) => setCreating({ ...creating, name: e.target.value, error: undefined })} />
            <Field id="rdCatPh" placeholder={"占位符，默认 " + defaultPlaceholder(creating.name || "类别名称")} aria-label="占位符" maxLength={64} value={creating.placeholder} onChange={(e) => setCreating({ ...creating, placeholder: e.target.value, error: undefined })} />
            <div className="newcat-actions">
              {creating.error && <span className="newcat-err">{creating.error}</span>}
              <Button variant="ghost" onClick={() => setCreating(null)}>
                取消
              </Button>
              <Button onClick={() => void create()}>创建</Button>
            </div>
          </div>
        )}
      </ModalBody>
      <ModalFooter>
        {index != null && (
          <Button variant="danger" style={pushLeft} onClick={() => deleteRule(index, draft)}>
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

/**
 * A change would leave schemes unable to start a task: the schemes and what they would lose are listed, and the
 * change is made only on a yes. Cancelling changes nothing (and returns to the dialog the change came from).
 */
export function DetectionLossDialog({ action, lost, noRules, go, back }: { action: string; lost: { preset: string; categories: CategoryId[] }[]; noRules: string[]; go: () => void; back?: ModalSpec }) {
  const catalog = useAppState((s) => s.categories);
  const names = [...new Set([...lost.map((x) => x.preset), ...noRules])];
  return (
    <Modal className="loss-modal" initialFocus=".btn-g">
      <ModalHeader title={"仍要" + action + "？"} subtitle={"以下方案将无法开始任务：" + names.map((n) => "「" + n + "」").join("、")} />
      <ModalBody>
        <ul className="loss-list">
          {lost.map((x) => (
            <li key={x.preset}>
              方案『{x.preset}』将无法开始任务：{x.categories.map((c) => categoryName(catalog, c)).join("、")} 没有检测来源
            </li>
          ))}
          {noRules.map((n) => (
            <li key={"rules:" + n}>方案『{n}』将没有任何启用的规则，开始任务会失败</li>
          ))}
        </ul>
        <div className="muted sm" style={{ marginTop: 10 }}>
          请为这些实体保留检测来源，并为方案保留至少一条启用的规则；也可在方案页调整处理方式。
        </div>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={() => (back ? openModal(back) : closeModal())}>
          取消
        </Button>
        <Button variant="danger" onClick={go}>
          仍要{action}
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
