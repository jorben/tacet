import { showModels } from "../../actions/models";
import { requestClose } from "../../actions/window";
import { getService } from "../../services";
import { useAppState } from "../../store";
import { Button, Icon } from "../../ui";
import { tip } from "../../ui/tip";
import { backTarget, windowTitle } from "./chrome";

export function TitleBar() {
  const host = getService().host;
  const view = useAppState((s) => s.view);
  const single = useAppState((s) => s.single);
  const batch = useAppState((s) => s.batch);
  const historyResult = useAppState((s) => s.historyResult);
  const navStack = useAppState((s) => s.navStack);
  const noModel = useAppState((s) => s.models.length > 0 && s.models.every((m) => m.status === "missing"));
  const downloading = useAppState((s) => s.models.some((m) => m.status === "downloading"));
  const chrome = { view, single, batch, historyResult, navStack };
  const title = windowTitle(chrome);
  const back = backTarget(chrome);
  const network = downloading ? "正在下载模型 · 文档留在本机" : "本机处理 · 未联网";
  return (
    <header className="titlebar">
      {host === "browser" && (
        <div className="lights">
          <button type="button" aria-label="关闭窗口" onClick={requestClose} />
          <button type="button" aria-label="最小化" tabIndex={-1} />
          <button type="button" aria-label="缩放" tabIndex={-1} />
        </div>
      )}
      <span id="tbBack">
        {back && (
          <Button variant="ghost" className="tb-back" onClick={back.go} aria-label={back.label} {...tip(back.label)}>
            <Icon name="chev-l" size={18} />
            返回
          </Button>
        )}
      </span>
      <div className="tb-title" id="tbTitle">
        {title}
      </div>
      <div className="tb-right">
        <Button variant="ghost" className="model-shell" id="modelShell" hidden={!noModel} aria-label="模型待下载，查看模型插件" onClick={() => showModels()}>
          <Icon name="cpu" size={13} />
          <span>模型待下载</span>
        </Button>
        <span id="networkState" className="ib" tabIndex={0} aria-label={network} {...tip(network, { align: "end" })}>
          <Icon name="shield" size={16} />
        </span>
      </div>
    </header>
  );
}
