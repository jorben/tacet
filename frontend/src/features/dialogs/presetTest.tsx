import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { runPresetTest, testErrorText } from "../../actions/presetTest";
import { MAX_TEST_TEXT, testCounts, testSegments, testSourceLabel, type TestHit, type TestResult } from "../../domain/presetTest";
import { formatConfidence, modelById } from "../../domain/presets";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Empty, List, Pill, Swatch, Tag, TextArea, categoryColor, pushLeft, tip } from "../../ui";

const SAMPLE = [
  "联系人：王晓明，手机 13812345678，邮箱 wangxm@example.com。",
  "身份证号 11010519491231002X，于 2026年3月15日 签约，收款卡号 6222020200112233446。",
  "办公地址：上海市浦东新区世纪大道100号，内网 10.12.0.8，文档见 https://wiki.example.com/p/42。",
].join("\n");

type Phase =
  | { kind: "idle" }
  | { kind: "running" }
  | { kind: "done"; text: string; result: TestResult }
  | { kind: "error"; message: string };

/** What the finding is and where it comes from, for the tooltip of the highlighted text. */
const hitTip = (h: TestHit): string =>
  h.label + " · " + testSourceLabel(h) + (h.model ? " " + formatConfidence(h.model.score) : "") + (h.rules.length ? " · " + h.rules.join("、") : "");

function SourceTags({ hit }: { hit: TestHit }) {
  return (
    <>
      {hit.model && (
        <Tag primary data-source="model" {...tip("模型置信度 " + formatConfidence(hit.model.score), { align: "end" })}>
          模型 {formatConfidence(hit.model.score)}
        </Tag>
      )}
      {hit.rules.length > 0 && <Tag data-source="rule">规则 · {hit.rules.join("、")}</Tag>}
    </>
  );
}

function Results({ text, result }: { text: string; result: TestResult }) {
  const { hits } = result;
  if (!hits.length) {
    return (
      <Empty padding="24px 0">
        按当前方案未识别到内容
        {result.minConfidence != null && <div className="muted sm">低于最低置信度 {formatConfidence(result.minConfidence)} 的模型结果也会列出</div>}
      </Empty>
    );
  }
  const c = testCounts(hits);
  const parts = [c.model && "模型 " + c.model, c.rules && "规则 " + c.rules, c.both && "模型 + 规则 " + c.both].filter(Boolean);
  return (
    <>
      <div className="pt-sum" aria-live="polite">
        识别到 {c.total} 处 · {parts.join(" · ")}
        {c.below > 0 && result.minConfidence != null && (
          <span className="pt-below">，{c.below} 处低于最低置信度 {formatConfidence(result.minConfidence)}，任务中不会涂抹</span>
        )}
      </div>
      <div className="pt-text" aria-label="识别结果">
        {testSegments(text, hits).map((s, i) => {
          if (s.hit == null) return <span key={i}>{s.text}</span>;
          const h = hits[s.hit]!;
          return (
            <mark key={i} className={h.model?.below && !h.rules.length ? "pt-hit below" : "pt-hit"} style={categoryColor(h.color)} {...tip(hitTip(h), { placement: "above" })}>
              {s.text}
            </mark>
          );
        })}
      </div>
      <List className="pt-list">
        {hits.map((h, i) => (
          <div className="row pt-row" style={categoryColor(h.color)} key={i}>
            <Swatch category={h.color} />
            <div className="pt-main">
              <div className="rl pt-val">{text.slice(h.start, h.end)}</div>
              <div className="rs">{h.label}</div>
            </div>
            <span className="rv">
              {h.model?.below && !h.rules.length && <Pill tone="warn">低于最低置信度</Pill>}
              <SourceTags hit={h} />
            </span>
          </div>
        ))}
      </List>
    </>
  );
}

/** “测试”: paste a text, see what the scheme finds in it and whether the model or a rule found each one. */
export function PresetTestDialog() {
  const preset = useAppState((s) => s.presets.find((p) => p.id === s.presetSel) ?? s.presets[0]);
  const model = useAppState((s) => modelById(s.models, preset?.model));
  const [text, setText] = useState("");
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const run = useRef(0);
  useEffect(() => () => { run.current++; }, []); // a result that arrives after closing is dropped
  const tooLong = text.length > MAX_TEST_TEXT;
  const canRun = !!text.trim() && !tooLong && phase.kind !== "running";

  const edit = (v: string) => {
    setText(v);
    if (phase.kind !== "idle") { run.current++; setPhase({ kind: "idle" }); } // the result belonged to the old text
  };
  const go = async () => {
    if (!canRun || !preset) return;
    const id = ++run.current;
    const value = text;
    setPhase({ kind: "running" });
    try {
      const result = await runPresetTest(value);
      if (run.current === id) setPhase({ kind: "done", text: value, result });
    } catch (e) {
      if (run.current === id) setPhase({ kind: "error", message: testErrorText(e) });
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void go(); }
  };
  if (!preset) return null;
  const how = preset.det === "model" ? (model?.name ?? "模型") + " + 规则" : "仅规则";
  return (
    <Modal className="pt-modal" labelledBy="ptTitle" initialFocus="textarea">
      <ModalHeader titleId="ptTitle" title="测试方案" subtitle={`方案「${preset.name}」· ${how}`} />
      <ModalBody>
        <TextArea
          id="ptText"
          aria-label="测试文本"
          placeholder="粘贴一段文本，查看当前方案能识别出哪些内容"
          value={text}
          onChange={(e) => edit(e.target.value)}
          onKeyDown={onKey}
        />
        <div className="pt-meta">
          <span>文本只在本机识别，不会保存</span>
          <span className={tooLong ? "pt-over" : undefined}>
            {text.length.toLocaleString()} / {MAX_TEST_TEXT.toLocaleString()}
          </span>
        </div>
        {phase.kind === "running" && (
          <div className="pt-sum muted" aria-live="polite">
            识别中…{preset.det === "model" ? "首次识别需要加载模型，可能需要几秒" : ""}
          </div>
        )}
        {phase.kind === "error" && (
          <div className="pt-sum pt-err" role="alert">
            {phase.message}
          </div>
        )}
        {phase.kind === "done" && <Results text={phase.text} result={phase.result} />}
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" style={pushLeft} onClick={() => edit(SAMPLE)}>
          填入示例
        </Button>
        <Button variant="ghost" onClick={closeModal}>
          关闭
        </Button>
        <Button variant="primary" icon="scan" disabled={!canRun} onClick={() => void go()}>
          {phase.kind === "running" ? "识别中…" : "识别"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}
