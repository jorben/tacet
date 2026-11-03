import { useState } from "react";
import { useAppState } from "../../store";
import { copyRuleset, newRuleset, openRulesetMoreMenu, selectRuleset, setCategoryRulesOn, showInConfig, startImport, toggleRule } from "../../actions/presets";
import { openModal } from "../../overlay/store";
import { BOUNDARY_LABELS } from "../../domain/catalog";
import { categoryName } from "../../domain/categories";
import { availableRulesets, ruleGroups, ruleGroupCount, ruleSummary, type RuleGroup } from "../../domain/presets";
import type { Ruleset } from "../../domain/types";
import { Button, Empty, IconButton, List, SectionTitle, Switch, Tag, tip } from "../../ui";
import { TriCheckbox } from "../../ui/TriCheckbox";
import { CategoryRow, useCategoryFocus } from "./CategoryRow";
import { ConfigHeader, ReadonlyNotice, RefLinks } from "./ConfigHeader";
import { CatalogNotice, GuideCard } from "./ConfigStates";
import { ItemList } from "./ItemList";

const GROUP_HINT = "只影响本规则组内的规则；类别要不要处理，请在方案页设置";

const addRule = (r: Ruleset, category?: string) => openModal({ type: "ruleEditor", rulesetId: r.id, index: null, ...(category ? { category } : {}) });

/** One category of the rule set: a head with its counts, the switch of all its rules and “＋”, then its rules. */
function Group({ r, g, locked, expanded, onToggle }: { r: Ruleset; g: RuleGroup; locked: boolean; expanded: boolean; onToggle: () => void }) {
  const name = useAppState((s) => categoryName(s.categories, g.category));
  const { on, total } = ruleGroupCount(g);
  return (
    <section className="rule-group" data-category={g.category} aria-label={name}>
      <CategoryRow category={g.category} className="rule-group-head" tags={<span className="muted sm">{total} 条规则 · {on} 条启用</span>}>
        <span {...tip(GROUP_HINT, { align: "end" })}>
          <TriCheckbox checked={on === total ? true : on === 0 ? false : "mixed"} label={"启用类别 " + name} disabled={locked} onChange={() => setCategoryRulesOn(g.category, on !== total)} />
        </span>
        <IconButton icon="plus" size={15} label={"在 " + name + " 下添加规则"} tooltip="在此类别下添加规则" tooltipAlign="end" disabled={locked} onClick={() => addRule(r, g.category)} />
        <IconButton icon={expanded ? "chev-d" : "chev-r"} size={15} label={(expanded ? "收起 " : "展开 ") + name} aria-expanded={expanded} onClick={onToggle} />
      </CategoryRow>
      {expanded && g.items.map(({ rule: x, index }) => (
        <div className="row rule-row rule-child" key={index}>
          <div className="rule-main">
            <div className="rl"><span className="rule-name" title={x.name}>{x.name}</span><Tag>{x.kind}</Tag>
              {x.bd && <Tag {...tip("命中前后不能紧邻" + BOUNDARY_LABELS[x.bd], { align: "end" })}>边界 · {BOUNDARY_LABELS[x.bd]}</Tag>}
            </div>
            <div className={x.kind === "正则" ? "rs mono" : "rs"} title={ruleSummary(x)}>{ruleSummary(x)}</div>
          </div>
          <span className="rv">
            <IconButton icon="edit" size={15} label="编辑" disabled={locked} onClick={() => openModal({ type: "ruleEditor", rulesetId: r.id, index })} />
            <Switch checked={x.on} disabled={locked} label={"启用 " + x.name} onChange={() => toggleRule(index)} />
          </span>
        </div>
      ))}
    </section>
  );
}

/** Remount when the rule set changes: expansion belongs to this visit, keyed by rule set and entity. */
function RuleGroups({ r, locked }: { r: Ruleset; locked: boolean }) {
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const catalog = useAppState((s) => s.categories);
  const on = r.rules.filter((x) => x.on).length;
  return (
    <>
      <SectionTitle first>
        规则
        <span className="sec-note">{r.rules.length} 条，{on} 条启用</span>
        <span className="sp" />
        <Button variant="ghost" icon="plus" disabled={locked} onClick={() => addRule(r)}>添加规则</Button>
      </SectionTitle>
      <List className="rule-groups">
        {ruleGroups(r.rules, catalog).map((g) => {
          const expanded = !collapsed.has(g.category);
          return <Group key={g.category} r={r} g={g} locked={locked} expanded={expanded} onToggle={() => setCollapsed((was) => {
            const next = new Set(was); if (expanded) next.add(g.category); else next.delete(g.category); return next;
          })} />;
        })}
        {!r.rules.length && <Empty padding="36px 0">还没有规则。添加第一条规则后，规则组才会保存，并可在方案中选用</Empty>}
      </List>
    </>
  );
}

export function RulesEditor() {
  const rulesets = useAppState((s) => s.rulesets);
  const plugins = useAppState((s) => s.plugins);
  const presets = useAppState((s) => s.presets);
  const selected = useAppState((s) => s.rulesetSel);
  const configLoad = useAppState((s) => s.configLoad);
  const pane = useCategoryFocus("rules");
  const sets = availableRulesets(rulesets, plugins);
  const r = sets.find((x) => x.id === selected) || sets[0];
  const list = (
    <ItemList
      label="规则组"
      items={sets.map((x) => ({ id: x.id, icon: "hash", title: x.name, subtitle: x.ver + " · " + x.rules.length + " 条规则", locked: !!(x.builtin || x.plugin) }))}
      selectedId={r?.id}
      onSelect={selectRuleset}
      onCreate={newRuleset}
      onImport={() => startImport("ruleset")}
    />
  );
  if (!r) {
    return (
      <div className="split">
        {list}
        <div className="preset-detail">
          <GuideCard icon="hash" title="还没有规则组" actions={<><Button variant="primary" icon="plus" onClick={newRuleset}>新建规则组</Button><Button icon="folder" onClick={() => startImport("ruleset")}>导入</Button></>}>
            规则组是一组词表或正则，方案选用后按规则识别类别。
          </GuideCard>
        </div>
      </div>
    );
  }
  const locked = !!(r.builtin || r.plugin);
  const used = presets.filter((p) => p.rules.includes(r.id));
  const pluginName = r.plugin ? plugins.find((x) => x.id === r.plugin)?.name : undefined;
  return (
    <div className="split">
      {list}
      <div className="preset-detail" ref={pane} role="region" aria-label="规则内容" tabIndex={0}>
        <ConfigHeader
          name={r.name}
          badge={r.ver}
          badgePrimary={!locked}
          subtitle={used.length ? <>用于 <RefLinks items={used} onOpen={(id) => showInConfig({ tab: "scheme", id })} /></> : "未被方案使用"}
          notice={locked && <ReadonlyNotice onCopy={copyRuleset}>{r.plugin ? "来自插件" + (pluginName ? "「" + pluginName + "」" : "") + "，只读。" : "内建规则组，只读。"}</ReadonlyNotice>}
        >
          <Button icon="copy" onClick={copyRuleset}>复制</Button>
          <IconButton icon="more" label="更多" onClick={(e) => openRulesetMoreMenu(e.currentTarget)} />
        </ConfigHeader>
        <CatalogNotice />
        <RuleGroups key={JSON.stringify([configLoad, r.id])} r={r} locked={locked} />
      </div>
    </div>
  );
}
