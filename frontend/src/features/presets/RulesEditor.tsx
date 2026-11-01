import { useAppState } from "../../store";
import { copyRuleset, newRuleset, openRulesetMoreMenu, selectRuleset, startImport, toggleRule } from "../../actions/presets";
import { openModal } from "../../overlay/store";
import { BOUNDARY_LABELS, CATEGORIES } from "../../domain/catalog";
import { availableRulesets, ruleSummary } from "../../domain/presets";
import { Button, Empty, IconButton, List, Switch, Swatch, Tag, categoryColor, tip } from "../../ui";
import { ConfigHeader } from "./ConfigHeader";
import { ItemList } from "./ItemList";

/** Rule sets: the list on the left, the rules of the selected set on the right. */
export function RulesEditor() {
  const rulesets = useAppState((s) => s.rulesets);
  const plugins = useAppState((s) => s.plugins);
  const presets = useAppState((s) => s.presets);
  const selected = useAppState((s) => s.rulesetSel);
  const sets = availableRulesets(rulesets, plugins);
  const r = sets.find((x) => x.id === selected) || sets[0]!;
  const locked = !!(r.builtin || r.plugin);
  const used = presets.filter((p) => p.rules.includes(r.id)).map((p) => p.name);
  return (
    <div className="split">
      <ItemList
        items={sets.map((x) => ({ id: x.id, icon: "hash", title: x.name, subtitle: x.ver + (x.plugin ? " · 插件" : "") + " · " + x.rules.length + " 条规则" }))}
        selectedId={r.id}
        onSelect={selectRuleset}
        onCreate={newRuleset}
        onImport={() => startImport("ruleset")}
      />
      <div className="preset-detail" role="region" aria-label="规则内容" tabIndex={0}>
        <ConfigHeader
          name={r.name}
          version={r.ver}
          locked={locked}
          lockedHint={r.plugin ? "来自插件，复制后可编辑" : "内建规则集，复制后可编辑"}
          subtitle={used.length ? "用于 " + used.join("、") : "未被方案使用"}
          onCopy={copyRuleset}
          onMore={openRulesetMoreMenu}
        />
        <List>
          {r.rules.length ? (
            r.rules.map((x, i) => (
              <div className="row rule-row" style={categoryColor(x.cat)} key={i}>
                <Swatch category={x.cat} />
                <div className="rule-main">
                  <div className="rl">
                    <span className="rule-name" title={x.name}>{x.name}</span>
                    <Tag>{x.kind}</Tag>
                    {x.bd && <Tag {...tip("命中前后不能紧邻" + BOUNDARY_LABELS[x.bd], { align: "end" })}>边界 · {BOUNDARY_LABELS[x.bd]}</Tag>}
                  </div>
                  <div className={x.kind === "正则" ? "rs mono" : "rs"} title={ruleSummary(x)}>
                    {ruleSummary(x)}
                  </div>
                </div>
                <span className="rv">
                  <span className="muted sm">{CATEGORIES[x.cat].l}</span>
                  {!locked && <IconButton icon="edit" size={15} label="编辑" onClick={() => openModal({ type: "ruleEditor", rulesetId: r.id, index: i })} />}
                  <Switch checked={x.on} disabled={locked} label={"启用 " + x.name} onChange={() => toggleRule(i)} />
                </span>
              </div>
            ))
          ) : (
            <Empty padding="36px 0">还没有规则</Empty>
          )}
        </List>
        {!locked && (
          <Button icon="plus" style={{ marginTop: 12 }} onClick={() => openModal({ type: "ruleEditor", rulesetId: r.id, index: null })}>
            添加规则
          </Button>
        )}
      </div>
    </div>
  );
}
