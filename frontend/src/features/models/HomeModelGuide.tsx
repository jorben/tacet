import { showModels } from "../../actions/models";
import { useAppState } from "../../store";
import { modelIsBusy } from "../../store/selectors";
import { Button, Icon } from "../../ui";
import { ModelCard } from "./ModelCard";

/** The notice on the home page that nudges the user to prepare a model (or shows the operation waiting for one). */
export function HomeModelGuide() {
  const models = useAppState((s) => s.models);
  const pending = useAppState((s) => s.modelPending);
  if (pending) return <ModelCard modelId={pending.modelId} />;
  const ready = models.filter((m) => m.status === "ready").length;
  const busy = models.filter((m) => modelIsBusy(models, m.id));
  const errors = models.filter((m) => m.status === "error");
  const paused = models.filter((m) => m.status === "paused");
  if (ready && !busy.length && !errors.length && !paused.length) return null;
  const desc = busy.length
    ? "正在准备识别模型，可先选择文档。"
    : errors.length
      ? "模型准备需要处理，请前往插件页查看。"
      : paused.length
        ? "模型下载已暂停，已下载的部分会保留。"
        : "请先下载隐私识别模型，以便支持更优识别效果和本地离线使用";
  return (
    <section className={"model-card home-model-notice " + (errors.length ? "error" : "")} data-home-guide aria-label="模型准备引导">
      <span className="model-icon" aria-hidden="true">
        <Icon name={errors.length ? "alert" : "cpu"} size={16} />
      </span>
      <p className="model-desc" role="status" aria-live="polite" aria-atomic="true">
        {desc}
      </p>
      <div className="model-actions">
        <Button variant="primary" onClick={() => showModels()}>
          {busy.length ? "查看进度" : errors.length ? "查看并处理" : paused.length ? "继续准备" : "前往下载"}
        </Button>
      </div>
    </section>
  );
}
