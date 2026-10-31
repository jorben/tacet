import { checkForUpdate, setSetting, startUpdate } from "../../actions/settings";
import { useAppState } from "../../store";
import { Button, Row, Switch } from "../../ui";

const STATUS_NOTE = { idle: "", checking: "", available: "", updating: "", latest: "已是最新版本" } as const;

/** Current version with the update check, and the switch for automatic checks. */
export function UpdateRows() {
  const update = useAppState((s) => s.update);
  const autoUpd = useAppState((s) => s.settings.autoUpd);
  const busy = update.status === "checking" || update.status === "updating";
  const note = update.status === "available" ? "发现新版本 " + update.latest : update.status === "updating" ? "正在更新至 " + update.latest + "…" : STATUS_NOTE[update.status];
  return (
    <>
      <Row
        icon="info"
        title="当前版本"
        description={note}
        descriptionProps={{ id: "updateStatus", role: "status", "aria-live": "polite", "aria-atomic": true }}
        controlsClassName="version-controls"
      >
        <button type="button" className="text-link" id="checkUpdate" disabled={busy} onClick={checkForUpdate}>
          {update.status === "checking" ? "检查中…" : "检查更新"}
        </button>
        <span className="mono" id="currentVersion">
          {update.current}
        </span>
        {(update.status === "available" || update.status === "updating") && (
          <Button variant="primary" id="updateApp" disabled={update.status === "updating"} onClick={startUpdate}>
            {update.status === "updating" ? "更新中…" : "更新"}
          </Button>
        )}
      </Row>
      <Row icon="clock" title="自动检查更新" description="开启后联网检查 · 仅发送版本号、平台与更新通道">
        <Switch id="autoUpdate" checked={autoUpd} label="自动检查更新" onChange={() => setSetting("autoUpd", !autoUpd)} />
      </Row>
    </>
  );
}
