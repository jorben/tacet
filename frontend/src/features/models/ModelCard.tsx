import { useEffect, useRef } from "react";
import { cancelPendingOperation, continuePendingOperation, downloadModel, pauseModel, prepareModel, showModels } from "../../actions/models";
import { MODEL_ERRORS, MODEL_LABELS } from "../../domain/catalog";
import { modelById } from "../../domain/presets";
import type { ModelPlugin } from "../../domain/types";
import { useAppState } from "../../store";
import { contextModelId, modelIsBusy } from "../../store/selectors";
import { Button, Icon, Pill } from "../../ui";

const TITLES: Record<Exclude<ModelPlugin["status"], "error">, string> = {
  missing: "此方案需要的模型尚未下载",
  downloading: "正在下载此方案需要的模型",
  paused: "模型下载已暂停",
  verifying: "下载完成，正在校验",
  loading: "正在准备本地识别",
  ready: "此方案需要的模型已就绪",
};
const DESCRIPTIONS: Record<Exclude<ModelPlugin["status"], "error">, string> = {
  missing: "下载后支持本地离线使用，文档始终留在本机。",
  downloading: "可以先选择文档、浏览方案。此模型准备好后再开始检测。",
  paused: "已下载的部分会保留，继续时无需从头下载。",
  verifying: "正在检查此模型的文件是否完整，完成后进行本地试运行。",
  loading: "正在加载并用内置样例试运行，不会读取你的文档。",
  ready: "已通过文件校验与本地试运行，支持本地离线使用。",
};

interface ModelCardProps {
  /** Management view (plugin page) instead of the task hint. */
  manage?: boolean;
  /** The model to show; defaults to the one the current screen is about. */
  modelId?: string | null;
}

/** The state of one local model and what the user can do about it, including an operation waiting for it. */
export function ModelCard({ manage = false, modelId }: ModelCardProps) {
  const models = useAppState((s) => s.models);
  const pending = useAppState((s) => s.modelPending);
  const contextId = useAppState(contextModelId);
  const id = (modelId === undefined ? contextId : modelId) || models.find((m) => modelIsBusy(models, m.id))?.id;
  const m = modelById(models, id);
  const p = m && pending?.modelId === m.id ? pending : null;
  const ref = useRef<HTMLElement>(null);
  const waiting = !!p;
  useEffect(() => {
    if (waiting) ref.current?.scrollIntoView({ block: "nearest" });
  }, [waiting]);

  if (!m) {
    return id ? (
      <section ref={ref} className="model-card error" data-model={id} aria-label="模型不可用">
        <div className="model-title">所选模型不可用</div>
        <p className="model-desc">请在插件页检查此模型是否仍受支持，原任务不会自动替换模型。</p>
        <Button onClick={() => showModels()}>查看模型</Button>
      </section>
    ) : null;
  }
  const st = m.status;
  const error = st === "error";
  if (st === "ready" && !p && !manage) return null;
  const detail = error ? MODEL_ERRORS[m.error || "offline"] : null;
  const title = manage ? m.name : detail ? detail[0] : TITLES[st as keyof typeof TITLES];
  const desc = detail ? detail[1] : DESCRIPTIONS[st as keyof typeof DESCRIPTIONS];
  const showTags = manage && !["missing", "ready"].includes(st);
  const busy = modelIsBusy(models);

  return (
    <section
      ref={ref}
      className={"model-card " + (error ? "error" : st === "missing" || (manage && st === "ready") ? "intro" : "")}
      data-model={m.id}
      data-manage={manage}
      aria-label={m.name + " 模型准备"}
    >
      <div className="model-head">
        <span className="model-icon">
          <Icon name={manage ? "cpu" : error ? "alert" : st === "ready" ? "check" : "cpu"} size={18} />
        </span>
        <div className="model-copy">
          <div className="model-title" role="status" aria-live="polite" aria-atomic="true">
            {title}
            {showTags && (
              <span className="model-tags">
                <Pill tone={error ? "warn" : "ghost"}>{MODEL_LABELS[st]}</Pill>
              </span>
            )}
          </div>
          {!(manage && !error) && (
            <p className="model-desc">
              {manage && error ? detail![0] + " · " : ""}
              {desc}
            </p>
          )}
          <div className="model-meta">
            {manage ? "" : (p?.preset || "本次任务") + " · " + m.name + " · "}
            {m.version} · {m.variant} · {m.languages}
            {st === "missing" && m.weightMiB ? " · 权重约 " + m.weightMiB + " MiB" : ""}
          </div>
          {(st === "downloading" || st === "paused") && (
            <div className="model-progress">
              <progress max={100} value={m.progress} aria-label={m.name + " 下载进度"} />
              <div className="model-progress-label" style={{ justifyContent: "flex-end" }}>
                <span className="num">{m.progress}%</span>
              </div>
            </div>
          )}
          <ModelActions model={m} manage={manage} />
        </div>
      </div>
      {p && (
        <div className="model-pending">
          <span>
            {p.label}
            {st === "ready" ? " · 准备好了，可以继续" : " · 等待 " + m.name + " 准备，尚未开始检测"}
          </span>
          <Button variant="ghost" onClick={cancelPendingOperation}>
            取消本次操作
          </Button>
          <Button variant="primary" disabled={!(st === "ready" && !busy)} onClick={continuePendingOperation}>
            继续处理
          </Button>
        </div>
      )}
    </section>
  );
}

function ModelActions({ model: m, manage }: { model: ModelPlugin; manage: boolean }) {
  const st = m.status;
  const error = st === "error";
  const detail = error ? MODEL_ERRORS[m.error || "offline"] : null;
  const actions = [];
  if (st === "missing" || st === "paused" || error) {
    const retryLoad = error && m.error === "load";
    actions.push(
      <Button key="main" variant="primary" onClick={() => (retryLoad ? prepareModel(m.id) : downloadModel(m.id))}>
        {detail ? detail[2] : st === "paused" ? "继续下载" : "下载模型"}
      </Button>,
    );
    if (retryLoad) actions.push(<Button key="again" variant="ghost" onClick={() => downloadModel(m.id)}>重新下载</Button>);
  }
  if (st === "downloading") actions.push(<Button key="pause" variant="ghost" onClick={() => pauseModel(m.id)}>暂停</Button>);
  if (!manage) actions.push(<Button key="view" variant="ghost" onClick={() => showModels(m.id)}>查看模型</Button>);
  if (manage && st === "ready") {
    actions.push(
      <span key="ready" className="model-ready" role="status">
        已就绪
      </span>,
    );
  }
  return actions.length ? <div className="model-actions">{actions}</div> : null;
}
