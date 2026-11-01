import { useEffect, useState } from "react";
import { openModal } from "../../overlay/store";
import { openSettingsKeepMenu, openWorkspaceDirectory, setSetting, setThemePref } from "../../actions/settings";
import { liveEngine } from "../../actions/live";
import { useAppState } from "../../store";
import { Button, Icon, List, Page, PillButton, Row, SectionTitle, SelectButton, Segmented } from "../../ui";
import { RedactStyleGallery } from "./RedactStyleGallery";
import { UpdateRows } from "./UpdateRows";

export function SettingsPage() {
  const themePref = useAppState((s) => s.themePref);
  const settings = useAppState((s) => s.settings);
  return (
    <Page id="settingsPage">
      <div className="h1" style={{ marginBottom: 18 }}>
        设置
      </div>
      <List>
        <Row icon="moon" title="外观">
          <Segmented
            value={themePref}
            onChange={setThemePref}
            options={[{ value: "light", label: "浅色" }, { value: "dark", label: "深色" }, { value: "system", label: "跟随系统" }]}
          />
        </Row>
        <Row icon="x" title="点击关闭按钮" description={settings.closeAct === "tray" ? "任务在后台继续" : "进行中的任务会暂停"}>
          <Segmented
            value={settings.closeAct}
            onChange={(v) => setSetting("closeAct", v)}
            options={[{ value: "tray", label: "最小化到托盘" }, { value: "quit", label: "关闭程序" }]}
          />
        </Row>
        <Row icon="cpu" title="资源档位" description={settings.tier === "comfort" ? "CPU ≤ 50% · 内存 2 GiB" : "内存 4 GiB · 更快"}>
          <Segmented value={settings.tier} onChange={(v) => setSetting("tier", v)} options={[{ value: "comfort", label: "舒适" }, { value: "std", label: "标准" }]} />
        </Row>
        <Row icon="clock" title="中断后可恢复">
          <SelectButton onClick={(e) => openSettingsKeepMenu(e.currentTarget)}>{settings.keep === "0" ? "不保留" : "保留 " + settings.keep + " 天"}</SelectButton>
        </Row>
      </List>
      <SectionTitle>涂抹样式</SectionTitle>
      <RedactStyleGallery value={settings.redact || "black"} />
      <SectionTitle>存储</SectionTitle>
      <List>
        <WorkspaceRow />
      </List>
      <SectionTitle>版本</SectionTitle>
      <List>
        <UpdateRows />
      </List>
      <SectionTitle>关于</SectionTitle>
      <List>
        <Row icon="globe" title="官网" onClick={() => openModal({ type: "aboutLink", which: "website" })}>
          <Icon name="ext" size={15} />
        </Row>
        <Row icon="scale" title="开源许可" onClick={() => openModal({ type: "licenses" })}>
          <span className="rs">Apache-2.0</span>
          <Icon name="chev-r" size={15} />
        </Row>
        <Row icon="users" title="社区">
          <PillButton tone="ghost" className="community-link" onClick={() => openModal({ type: "aboutLink", which: "discord" })}>
            <Icon name="discord" size={15} />
            加入 Discord
          </PillButton>
          <PillButton tone="ghost" className="community-link" onClick={() => openModal({ type: "aboutLink", which: "github" })}>
            <Icon name="github" size={15} />
            GitHub
          </PillButton>
        </Row>
      </List>
    </Page>
  );
}

function WorkspaceRow() {
  const recoverable = useAppState((s) => s.history.filter((h) => h.st === "keep").length);
  const engine = liveEngine();
  const [size, setSize] = useState(engine ? "计算中…" : "1.4 GiB");
  const [opening, setOpening] = useState(false);
  useEffect(() => {
    if (!engine) return;
    let alive = true;
    engine.workspaceInfo().then((info) => {
      if (alive) setSize((info.bytes / 1024 ** 3).toFixed(1) + " GiB");
    }).catch(() => { if (alive) setSize("容量暂不可用"); });
    return () => { alive = false; };
  }, [engine, recoverable]);
  const openDirectory = async () => {
    setOpening(true);
    try { await openWorkspaceDirectory(); }
    finally { setOpening(false); }
  };
  return (
    <Row icon="hdd" title="工作区" description={"加密保护中 · " + size + " · " + recoverable + " 可恢复任务"}>
      <Button disabled={opening} onClick={() => void openDirectory()}>{opening ? "正在打开…" : "打开目录"}</Button>
      <Button onClick={() => openModal({ type: "cleanWorkspace" })}>清理…</Button>
    </Row>
  );
}
