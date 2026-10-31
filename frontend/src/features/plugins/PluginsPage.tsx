import { Fragment } from "react";
import { useScrollRequests } from "../../actions/scrollRequests";
import { openModal } from "../../overlay/store";
import { openPluginMenu, togglePlugin } from "../../actions/plugins";
import type { PluginInfo } from "../../domain/types";
import { useAppState } from "../../store";
import { Button, Empty, IconButton, IconTile, List, Page, PageHead, SectionTitle, Switch, Tag } from "../../ui";
import { ModelCard } from "../models/ModelCard";

const ROLE_ICON: Record<string, string> = { 格式: "file", 模型: "cpu", 规则: "hash" };

function PluginRow({ plugin: p }: { plugin: PluginInfo }) {
  return (
    <div className="row">
      <IconTile icon={ROLE_ICON[p.role] || "puzzle"} size={32} />
      <div style={{ minWidth: 0 }}>
        <div className="rl" style={{ display: "flex", alignItems: "center", gap: 8 }}>
          {p.name}
          {!p.builtin && (
            <>
              <Tag>{p.role}</Tag>
              <Tag primary data-tip="在系统沙箱中运行，无网络">
                沙箱
              </Tag>
            </>
          )}
        </div>
        <div className="rs">
          {p.fmt ? p.fmt + " · " : ""}
          {p.ver}
          {p.src && (
            <>
              {" · "}
              <span className="mono">
                {p.src} @ {p.commit}
              </span>
            </>
          )}
        </div>
      </div>
      <span className="rv">
        {!p.builtin && <IconButton icon="more" label="更多" onClick={(e) => openPluginMenu(e.currentTarget, p.id)} />}
        <Switch checked={p.on} label={"启用 " + p.name} onChange={() => togglePlugin(p.id)} />
      </span>
    </div>
  );
}

export function PluginsPage() {
  const plugins = useAppState((s) => s.plugins);
  const models = useAppState((s) => s.models);
  useScrollRequests((r) => {
    const card = r.kind === "model" ? document.querySelector(`.model-card[data-model="${r.id}"]`) : null;
    (card || document.getElementById("modelPlugins"))?.scrollIntoView({ block: "center" });
  });
  const builtin = plugins.filter((p) => p.builtin);
  const third = plugins.filter((p) => !p.builtin);
  return (
    <Page>
      <PageHead title="插件">
        <Button variant="primary" icon="plus" onClick={() => openModal({ type: "installPlugin" })}>
          安装
        </Button>
      </PageHead>
      <section id="modelPlugins" aria-label="模型">
        <SectionTitle first>
          模型<span className="model-section-note">按需下载，在方案中选择使用</span>
        </SectionTitle>
        {models.map((m) => (
          <ModelCard key={m.id} manage modelId={m.id} />
        ))}
      </section>
      {(["格式", "规则"] as const).map((role, i) => {
        const xs = builtin.filter((p) => p.role === role);
        return xs.length ? (
          <Fragment key={role}>
            <SectionTitle first={i === 0}>{role}</SectionTitle>
            <List>
              {xs.map((p) => (
                <PluginRow key={p.id} plugin={p} />
              ))}
            </List>
          </Fragment>
        ) : null;
      })}
      <SectionTitle>第三方</SectionTitle>
      <List>
        {third.length ? third.map((p) => <PluginRow key={p.id} plugin={p} />) : <Empty padding="28px">暂无</Empty>}
      </List>
    </Page>
  );
}
