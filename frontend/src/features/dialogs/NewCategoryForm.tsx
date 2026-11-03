import { useState } from "react";
import { createCategory, defaultPlaceholder } from "../../actions/categories";
import { Button, Field } from "../../ui";

const CREATE_ERRORS = { name: "请填写类别名称（至多 32 个字）", placeholder: "占位符至多 64 个字，且不能换行", taken: "已有同名类别", failed: "未能创建，请重试" } as const;

/**
 * Makes a category where one is picked (what a rule reports, what a model's labels count as): a name and a
 * placeholder ("[名称]" unless one is written). Categories are one catalog shared by every rule set, model
 * and scheme. `onCreated` gets the new category's id.
 */
export function NewCategoryForm({ idPrefix, onCreated, onCancel }: { idPrefix: string; onCreated: (id: string) => void; onCancel: () => void }) {
  const [name, setName] = useState("");
  const [placeholder, setPlaceholder] = useState("");
  const [error, setError] = useState<string>();
  const create = async () => {
    const got = await createCategory(name, placeholder.trim() || defaultPlaceholder(name));
    if ("id" in got) onCreated(got.id);
    else setError(CREATE_ERRORS[got.error]);
  };
  return (
    <div className="newcat" role="group" aria-label="新建类别">
      <Field id={idPrefix + "CatName"} placeholder="类别名称，如 项目代号" aria-label="类别名称" maxLength={32} value={name} onChange={(e) => { setName(e.target.value); setError(undefined); }} />
      <Field id={idPrefix + "CatPh"} placeholder={"占位符，默认 " + defaultPlaceholder(name || "类别名称")} aria-label="占位符" maxLength={64} value={placeholder} onChange={(e) => { setPlaceholder(e.target.value); setError(undefined); }} />
      <div className="muted sm">类别为全局共用，规则组、模型与方案都会看到它</div>
      <div className="newcat-actions">
        {error && <span className="newcat-err">{error}</span>}
        <Button variant="ghost" onClick={onCancel}>
          取消
        </Button>
        <Button onClick={() => void create()}>创建</Button>
      </div>
    </div>
  );
}
