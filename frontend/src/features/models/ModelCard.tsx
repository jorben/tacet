import { useEffect, useRef } from "react";
import {
  cancelPendingOperation, continuePendingOperation, downloadAccel, downloadModel, homeRelative, pauseAccel, hubUrl, openModelDirectory, openModelPage, pauseModel, prepareModel, showModels,
  splitPath,
} from "../../actions/models";
import { MODEL_ERRORS, MODEL_LABELS } from "../../domain/catalog";
import { modelById } from "../../domain/presets";
import type { ModelPlugin } from "../../domain/types";
import { useAppState } from "../../store";
import { contextModelId, modelIsBusy } from "../../store/selectors";
import { Button, Icon, IconButton, Pill, PillButton } from "../../ui";

const TITLES: Record<Exclude<ModelPlugin["status"], "error">, string> = {
  missing: "此方案需要的模型尚未下载",
  downloading: "正在下载此方案需要的模型",
  paused: "模型下载已暂停",
  verifying: "正在校验模型文件",
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
  const stateTone = error ? "warn" : st === "ready" ? "ok" : st === "missing" || st === "paused" ? "ghost" : "pri";
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
          <div className="model-titlebar">
          <div className="model-title" {...(manage ? {} : { role: "status", "aria-live": "polite" as const, "aria-atomic": true })}>
            {title}
            {manage && m.runtime && hubUrl(m.runtime.repository) && (
              <PillButton
                tone="ghost"
                className="model-hf"
                title={"在 Hugging Face 查看 " + m.runtime.repository}
                aria-label={"在浏览器中打开 " + m.name + " 的 Hugging Face 模型页"}
                onClick={() => openModelPage(m.runtime!.repository)}
              >
                HF
                <Icon name="ext" size={12} />
              </PillButton>
            )}
          </div>
          {manage && (
            <Pill tone={stateTone} dot={stateTone === "pri"} className="model-state" role="status" aria-atomic="true" aria-label={m.name + "：" + MODEL_LABELS[st]}>
              {MODEL_LABELS[st]}
            </Pill>
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
            {(manage || st === "missing") && m.weightMiB ? " · 权重约 " + sizeLabel(m.weightMiB) : ""}
          </div>
          {manage && st === "missing" && m.eligible === false && (
            <p className="model-note warn" role="note">需要至少 {m.minMemoryGiB || 16} GiB 内存，本机不满足，无法下载。</p>
          )}
          {(st === "downloading" || st === "paused") && (
            <div className="model-progress">
              <progress max={100} value={m.progress} aria-label={m.name + " 下载进度"} />
              <div className="model-progress-label" style={{ justifyContent: "flex-end" }}>
                <span className="num">{m.progress}%</span>
              </div>
            </div>
          )}
          {manage ? (
            // where the files are and what they run on, with the actions at the end of the same band
            <div className="model-foot">
              {m.runtime && <ModelRuntime id={m.id} runtime={m.runtime} />}
              <ModelActions model={m} manage={manage} />
            </div>
          ) : (
            <ModelActions model={m} manage={manage} />
          )}
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

/** Where the model files are and whether inference runs on the CPU or a GPU. */
/** "36 MiB" or "1.5 GiB". */
const sizeLabel = (mib: number): string => (mib >= 1024 ? (mib / 1024).toFixed(1) + " GiB" : mib + " MiB");

/** Where the model files are (once downloaded) and whether inference runs on the CPU or a GPU. */
function ModelRuntime({ id, runtime: r }: { id: string; runtime: NonNullable<ModelPlugin["runtime"]> }) {
  const [head, tail] = splitPath(homeRelative(r.dir));
  return (
    <dl className="model-runtime">
      <div>
        <dt>推理设备</dt>
        <dd title={r.device === "auto" && !r.detecting && r.deviceCode ? r.deviceCode : r.backend}>{r.device === "auto" ? (r.detecting ? "GPU 优先 · 正在检测" : r.deviceCode ? "GPU 优先 · 检测未完成，首次任务时确认" : "GPU 优先 · 首次任务时确认") : r.device === "gpu" ? "GPU · " + ({ CoreMLExecutionProvider: "Core ML", DmlExecutionProvider: "DirectML", CUDAExecutionProvider: "CUDA", ROCMExecutionProvider: "ROCm" }[r.backend] || "ONNX Runtime") : "CPU · ONNX Runtime"}</dd>
      </div>
      {r.dir && <div>
        <dt>存储位置</dt>
        <dd className="model-path">
          <span
            className="model-path-text mono"
            title={r.dir}
            onCopy={(e) => { e.clipboardData.setData("text/plain", r.dir); e.preventDefault(); }}
          >
            <span className="model-path-head">{head}</span>
            <span className="model-path-tail">{tail}</span>
          </span>
          <IconButton icon="folder" size={14} className="model-path-open" label="打开存储位置" tooltip="打开存储位置" tooltipAlign="end" onClick={() => void openModelDirectory(id)} />
        </dd>
      </div>}
      {r.accel && <AccelRow id={id} runtime={r} accel={r.accel} />}
    </dl>
  );
}

/** The GPU build of the model: its own download, apart from the GPU acceleration setting that decides its use. */
function AccelRow({ id, runtime: r, accel: a }: { id: string; runtime: NonNullable<ModelPlugin["runtime"]>; accel: NonNullable<NonNullable<ModelPlugin["runtime"]>["accel"]> }) {
  const st = a.status;
  const state =
    st === "ready" ? (r.accelerated ? "使用中" : "已下载 · GPU 加速未开启")
    : st === "missing" ? "未下载"
    : st === "downloading" ? "下载中 " + a.progress + "%"
    : st === "paused" ? "已暂停 " + a.progress + "%"
    : st === "verifying" ? "校验中"
    : st === "loading" ? "编译与试运行中"
    : MODEL_ERRORS[a.error || "offline"][0];
  const tone = st === "ready" ? (r.accelerated ? "ok" : "") : st === "error" ? "warn" : st === "missing" ? "" : "pri";
  const action =
    st === "missing" ? "下载" : st === "paused" ? "继续" : st === "error" ? MODEL_ERRORS[a.error || "offline"][2] : st === "downloading" ? "暂停" : "";
  return (
    <div>
      <dt>GPU 版本</dt>
      <dd className="model-accel">
        <span>{a.variant} · {sizeLabel(a.weightMiB)}</span>
        <span className={"model-accel-state " + tone} role="status" aria-atomic="true">{state}</span>
        {action && (
          <button type="button" className="model-link" onClick={() => (st === "downloading" ? pauseAccel(id) : downloadAccel(id))}>
            {action}
          </button>
        )}
      </dd>
    </div>
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
      <Button key="main" variant="primary" disabled={st === "missing" && m.eligible === false} onClick={() => (retryLoad ? prepareModel(m.id) : downloadModel(m.id))}>
        {detail ? detail[2] : st === "paused" ? "继续下载" : "下载模型"}
      </Button>,
    );
    if (retryLoad) actions.push(<Button key="again" variant="ghost" onClick={() => downloadModel(m.id)}>重新下载</Button>);
  }
  if (st === "downloading") actions.push(<Button key="pause" variant="ghost" onClick={() => pauseModel(m.id)}>暂停</Button>);
  if (!manage) actions.push(<Button key="view" variant="ghost" onClick={() => showModels(m.id)}>查看模型</Button>);
  return actions.length ? <div className="model-actions">{actions}</div> : null;
}
