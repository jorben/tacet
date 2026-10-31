import { cancelBatch, openBatchImageMenu, openBatchKeepMenu, openBatchOutputMenu, openBatchSchemeMenu, setBatchMode, showUnprocessableFiles, startBatch, toggleAutoPublish } from "../../actions/batch";
import { MODEL_LABELS } from "../../domain/catalog";
import { fmt } from "../../domain/format";
import { findPresetByName, formatConfidence, modelById, presetConfidence, presetModelId, presetNeedsModel } from "../../domain/presets";
import type { Batch, BatchMode } from "../../domain/types";
import { useAppState } from "../../store";
import { Button, Icon, List, Page, Pill, PillButton, Row, SelectButton, Segmented, Switch } from "../../ui";
import { ModelCard } from "../models/ModelCard";

const MODE_TIPS: Record<BatchMode, string> = { guided: "自动处理，例外集中给你", auto: "例外先搁置，结束后集中处理", strict: "每份文件由你确认后才导出" };

/** Settings of a batch before it starts. */
export function PrecheckPage({ batch: B }: { batch: Batch }) {
  const presets = useAppState((s) => s.presets);
  const models = useAppState((s) => s.models);
  const pendingOperation = useAppState((s) => !!s.modelPending);
  const modelId = B.modelId || presetModelId(presets, B.preset);
  const model = modelById(models, modelId);
  const needsModel = presetNeedsModel(presets, B.preset);
  const modelReady = model?.status === "ready";
  const formats: Record<string, number> = {};
  B.files.forEach((f) => { if (f.st !== "bad") formats[f.ext] = (formats[f.ext] || 0) + 1; });
  const bad = B.files.filter((f) => f.st === "bad");
  const withImages = B.files.filter((f) => f.imgs).length;
  const imageCount = B.files.reduce((a, f) => a + f.imgs, 0);
  const busy = models.some((m) => ["downloading", "verifying", "loading"].includes(m.status));
  return (
    <Page>
      <div className="pc-head">
        <div className="big num">
          {fmt(B.n)}
          <span className="unit">份</span>
        </div>
        <div className="chips">
          {Object.entries(formats).map(([ext, n]) => (
            <Pill key={ext}>
              {ext.toUpperCase()} {fmt(n)}
            </Pill>
          ))}
          {bad.length > 0 && (
            <PillButton tone="bad" onClick={(e) => showUnprocessableFiles(e.currentTarget)}>
              <Icon name="alert" size={12} strokeWidth={2} />
              无法处理 {bad.length}
            </PillButton>
          )}
        </div>
      </div>
      <List>
        <Row icon="sliders" title="方式">
          <Segmented
            value={B.mode}
            onChange={setBatchMode}
            options={[
              { value: "guided", label: "引导", tooltip: MODE_TIPS.guided },
              { value: "auto", label: "无人值守", tooltip: MODE_TIPS.auto },
              { value: "strict", label: "严格", tooltip: MODE_TIPS.strict },
            ]}
          />
        </Row>
        <Row icon="layers" title="方案">
          <SelectButton onClick={(e) => openBatchSchemeMenu(e.currentTarget)}>{B.preset}</SelectButton>
        </Row>
        <Row icon="image" title="图像与嵌入" description={fmt(withImages) + " 份 · " + fmt(imageCount) + " 个"} descriptionClassName="num">
          <SelectButton onClick={(e) => openBatchImageMenu(e.currentTarget)}>{B.imgAct === "remove" ? "涂抹" : "保留 · 部分结果"}</SelectButton>
        </Row>
        <Row icon="folder" title="导出位置" description="T0001.pdf · T0002.docx …" descriptionClassName="mono">
          <SelectButton onClick={(e) => openBatchOutputMenu(e.currentTarget)}>{B.out}</SelectButton>
        </Row>
        <Row icon="send" title="验证后自动导出">
          <Switch checked={B.autoPub || B.mode === "auto"} disabled={B.mode === "auto"} label="验证后自动导出" onChange={toggleAutoPublish} />
        </Row>
        <Row icon="clock" title="中断后可恢复">
          <SelectButton onClick={(e) => openBatchKeepMenu(e.currentTarget)}>{B.keep === "0" ? "不保留" : "保留 " + B.keep + " 天"}</SelectButton>
        </Row>
      </List>
      {(needsModel || busy) && <ModelCard modelId={modelId} />}
      <div className="pc-foot">
        <span className="muted sm" style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <Icon name={needsModel && !modelReady ? "cpu" : "check"} size={13} strokeWidth={2.2} />
          {needsModel ? (model?.name || "所选模型") + " " + (model ? MODEL_LABELS[model.status] : "不可用") : "仅规则 · 无需模型"}
          {needsModel ? " · 置信度 ≥ " + formatConfidence(presetConfidence(findPresetByName(presets, B.preset), models)) : ""}
          {" · 需 " + (B.n * 0.015).toFixed(1) + " GiB / 可用 86 GiB"}
        </span>
        <span className="sp" />
        <Button variant="ghost" onClick={cancelBatch}>
          取消
        </Button>
        <Button variant="primary" large disabled={pendingOperation} onClick={startBatch}>
          {needsModel && !modelReady ? "准备模型后处理" : "开始处理"}
        </Button>
      </div>
    </Page>
  );
}
