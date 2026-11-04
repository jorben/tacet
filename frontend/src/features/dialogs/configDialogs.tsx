import { useRef, useState } from "react";
import {
  deleteRule, finishImport, importNameClashes, rollback, saveRule, versionTarget, type RuleDraft,
} from "../../actions/presets";
import { confirmDeleteCategory, deleteCategory } from "../../actions/categories";
import { categoryName, categoryOf } from "../../domain/categories";
import { categoryUse } from "../../domain/liveConfig";
import { NewCategoryForm } from "./NewCategoryForm";
import type { Boundary, CategoryId, RuleKind } from "../../domain/types";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal, openModal, type ModalSpec } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Field, Icon, IconButton, List, Segmented, Swatch, Tag, TextArea, pushLeft } from "../../ui";

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
  const [creating, setCreating] = useState(false);
  // deleting a category returns to this editor as it is (the category it reports cannot be deleted from here)
  const here: ModalSpec = { type: "ruleEditor", rulesetId, index, draft };
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
            <span key={c.id} className="typegrid-item">
              <button type="button" aria-pressed={draft.cat === c.id} onClick={() => set("cat", c.id)}>
                <Swatch category={c.id} />
                {c.name}
              </button>
              {c.origin === "custom" && draft.cat !== c.id && (
                <IconButton className="typegrid-del" icon="x" size={12} label={"删除类别 " + c.name} onClick={() => deleteCategory(c.id, here)} />
              )}
            </span>
          ))}
          {!creating && (
            <button type="button" className="typegrid-add" onClick={() => setCreating(true)}>
              <Icon name="plus" size={12} />
              新建类别
            </button>
          )}
        </div>
        {creating && <NewCategoryForm idPrefix="rd" onCreated={(id) => { set("cat", id); setCreating(false); }} onCancel={() => setCreating(false)} />}
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
 * A change would take the detection source of categories away in schemes that mask or replace them (they turn
 * off there until a source comes back), or leave a scheme no rule switched on: the schemes and what they would
 * lose are listed, and the change is made only on a yes. Cancelling changes nothing (and returns to the dialog
 * the change came from).
 */
export function DetectionLossDialog({ action, lost, noRules, go, back }: { action: string; lost: { preset: string; categories: CategoryId[] }[]; noRules: string[]; go: () => void; back?: ModalSpec }) {
  const catalog = useAppState((s) => s.categories);
  const names = [...new Set([...lost.map((x) => x.preset), ...noRules])];
  return (
    <Modal className="loss-modal" initialFocus=".btn-g">
      <ModalHeader title={"仍要" + action + "？"} subtitle={"会影响以下方案：" + names.map((n) => "「" + n + "」").join("、")} />
      <ModalBody>
        <ul className="loss-list">
          {lost.map((x) => (
            <li key={x.preset}>
              方案『{x.preset}』中的 {x.categories.map((c) => categoryName(catalog, c)).join("、")} 将没有检测来源，改为关闭
            </li>
          ))}
          {noRules.map((n) => (
            <li key={"rules:" + n}>方案『{n}』将没有任何启用的规则</li>
          ))}
        </ul>
        <div className="muted sm" style={{ marginTop: 10 }}>
          方案里的涂抹或占位设置会保留，重新有模型标签或规则识别这些类别后自动生效。
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

/**
 * Deleting a user category. Categories are shared: the dialog lists the models whose labels stop counting as it.
 * Tasks already started keep what they froze.
 */
export function DeleteCategoryDialog({ id, back }: { id: CategoryId; back?: ModalSpec }) {
  const s = useAppState((x) => x);
  const [busy, setBusy] = useState(false);
  const name = categoryName(s.categories, id);
  const use = categoryUse(id, s.rulesets, s.models);
  return (
    <Modal className="loss-modal" initialFocus=".btn-g">
      <ModalHeader title={"删除类别「" + name + "」？"} subtitle="类别为全局共用，删除对所有规则组、模型与方案生效" />
      <ModalBody>
        {use.models.length ? (
          <ul className="loss-list">
            {use.models.map((m) => (
              <li key={m.name}>模型『{m.name}』中关联到它的标签 {m.labels.join("、")} 将变为未关联</li>
            ))}
          </ul>
        ) : (
          <div>没有模型使用它。</div>
        )}
        <div className="muted sm" style={{ marginTop: 10 }}>
          已开始的任务与历史结果保持原样。
        </div>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={() => (back ? openModal(back) : closeModal())}>
          取消
        </Button>
        <Button variant="danger" disabled={busy} onClick={() => { setBusy(true); void confirmDeleteCategory(id, back); }}>
          删除
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
      <ModalHeader title="名称冲突" subtitle={`已有同名${kind === "ruleset" ? "规则组" : "方案"}「${name}」，导入不会覆盖或合并`} />
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
