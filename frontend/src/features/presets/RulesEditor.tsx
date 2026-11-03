import { useState } from "react";
import { useAppState } from "../../store";
import { copyRuleset, newRuleset, openRulesetMoreMenu, selectRuleset, setCategoryRulesOn, startImport, toggleRule } from "../../actions/presets";
import { openModal } from "../../overlay/store";
import { BOUNDARY_LABELS } from "../../domain/catalog";
import { categoryName } from "../../domain/categories";
import { availableRulesets, presetsWithoutSource, ruleGroups, ruleGroupCount, ruleSummary } from "../../domain/presets";
import type { Ruleset } from "../../domain/types";
import { Button, Empty, IconButton, List, Switch, Swatch, Tag, tip } from "../../ui";
import { TriCheckbox } from "../../ui/TriCheckbox";
import { ConfigHeader } from "./ConfigHeader";
import { ItemList } from "./ItemList";

const GROUP_HINT = "只影响本规则集内的规则；实体要不要处理，请在方案页设置";

/** Remount when the rule set changes: expansion belongs to this visit, keyed by rule set and entity. */
function RuleGroups({ r, locked }: { r: Ruleset; locked: boolean }) {
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const catalog = useAppState((s) => s.categories);
  const s = useAppState((s) => s);
  const loading = s.categoryStatus === "loading";
  const sources = { rulesets: availableRulesets(s.rulesets, s.plugins), models: s.models };
  return <List className="rule-groups">
    {ruleGroups(r.rules, loading ? [] : catalog).map((g) => {
      const key = JSON.stringify([r.id, g.category]);
      const { on, total } = ruleGroupCount(g);
      const multi = total > 1;
      const expanded = !collapsed.has(key);
      const name = loading ? g.category : categoryName(catalog, g.category);
      const lost = presetsWithoutSource(s.presets, sources, (p) => p.rules.includes(r.id), g.category);
      const entity = <><Swatch category={g.category} /><span className="entity-name">{name}</span>{!loading && !g.known && <Tag>未登记实体</Tag>}</>;
      return <section className="rule-group" data-category={g.category} key={key} aria-label={name}>
        {multi && <div className="row rule-group-head">
          <button type="button" className="rule-expand" aria-label={(expanded ? "收起 " : "展开 ") + name} aria-expanded={expanded} onClick={() => setCollapsed((was) => {
            const next = new Set(was); if (expanded) next.add(key); else next.delete(key); return next;
          })}><span aria-hidden="true">{expanded ? "▾" : "▸"}</span>{entity}</button>
          <span className="muted sm">{total} 条规则 · {on} 条启用</span>
          <span className="rv" {...tip(GROUP_HINT, { align: "end" })}>
            {on > 0 && on < total && <span className="muted sm">{on}/{total} 已启用</span>}
            <TriCheckbox checked={on === total ? true : on === 0 ? false : "mixed"} label={"启用实体 " + name} disabled={locked} onChange={() => setCategoryRulesOn(g.category, on !== total)} />
          </span>
        </div>}
        {(!multi || expanded) && g.items.map(({ rule: x, index }) => <div className={"row rule-row" + (multi ? " rule-child" : "")} key={index}>
          {!multi && entity}
          <div className="rule-main">
            <div className="rl"><span className="rule-name" title={x.name}>{x.name}</span><Tag>{x.kind}</Tag>
              {x.bd && <Tag {...tip("命中前后不能紧邻" + BOUNDARY_LABELS[x.bd], { align: "end" })}>边界 · {BOUNDARY_LABELS[x.bd]}</Tag>}
            </div>
            <div className={x.kind === "正则" ? "rs mono" : "rs"} title={ruleSummary(x)}>{ruleSummary(x)}</div>
          </div>
          <span className="rv">
            {!locked && <IconButton icon="edit" size={15} label="编辑" onClick={() => openModal({ type: "ruleEditor", rulesetId: r.id, index })} />}
            <span {...(!multi ? tip(GROUP_HINT, { align: "end" }) : {})}><Switch checked={x.on} disabled={locked} label={"启用 " + x.name} onChange={() => toggleRule(index)} /></span>
          </span>
        </div>)}
        {lost.map((p) => <div role="note" className="source-warning" key={p.id}>方案『{p.name}』将无法开始任务：该实体没有检测来源</div>)}
        {!locked && (!multi || expanded) && <div className="rule-add"><Button variant="ghost" icon="plus" onClick={() => openModal({ type: "ruleEditor", rulesetId: r.id, index: null, category: g.category })}>在此实体下添加规则</Button></div>}
      </section>;
    })}
    {!r.rules.length && <Empty padding="36px 0">还没有规则</Empty>}
  </List>;
}

export function RulesEditor() {
  const s = useAppState((s) => s);
  const sets = availableRulesets(s.rulesets, s.plugins);
  const r = sets.find((x) => x.id === s.rulesetSel) || sets[0];
  if (!r) return <Empty>还没有规则集</Empty>;
  const locked = !!(r.builtin || r.plugin);
  const used = s.presets.filter((p) => p.rules.includes(r.id)).map((p) => p.name);
  return <div className="split">
    <ItemList items={sets.map((x) => ({ id: x.id, icon: "hash", title: x.name, subtitle: x.ver + (x.plugin ? " · 插件" : "") + " · " + x.rules.length + " 条规则" }))} selectedId={r.id} onSelect={selectRuleset} onCreate={newRuleset} onImport={() => startImport("ruleset")} />
    <div className="preset-detail" role="region" aria-label="规则内容" tabIndex={0}>
      <ConfigHeader name={r.name} version={r.ver} locked={locked} lockedHint={r.plugin ? "来自插件，复制后可编辑" : "内建规则集，复制后可编辑"} subtitle={used.length ? "用于 " + used.join("、") : "未被方案使用"} onCopy={copyRuleset} onMore={openRulesetMoreMenu} />
      <RuleGroups key={JSON.stringify([s.configLoad, r.id])} r={r} locked={locked} />
      {!locked && <Button icon="plus" style={{ marginTop: 12 }} onClick={() => openModal({ type: "ruleEditor", rulesetId: r.id, index: null })}>添加规则</Button>}
    </div>
  </div>;
}
