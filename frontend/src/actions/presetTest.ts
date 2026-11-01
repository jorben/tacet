// “测试”: runs the selected scheme over a text the person types. On the desktop the kernel detects it exactly as
// a task would (the scheme is stored first, so unsaved edits count); in a browser the simulation stands in.
import { simulateTest, testFromEngine, type TestResult } from "../domain/presetTest";
import { availableRulesets } from "../domain/presets";
import { getState } from "../store";
import { errorText, liveEngine } from "./live";
import { flushConfig } from "./liveConfig";

const TEST_ERRORS: Record<string, string> = {
  MODEL_NOT_INSTALLED: "方案使用的模型尚未就绪，请先在插件页下载",
  MODEL_UNAVAILABLE: "方案使用的模型不可用",
  MODEL_START_FAILED: "模型未能启动，请稍后重试",
  TIMEOUT: "识别超时：模型可能正被任务占用，请稍后重试",
  INVALID_REQUEST: "文本过长，或方案配置不完整",
  NOT_FOUND: "方案尚未保存，请稍后重试",
};

const codeOf = (e: unknown): string => String((e as { message?: string })?.message ?? e).replace(/^.*?([A-Z][A-Z0-9_]{2,})\b.*$/s, "$1");

/** Why a test did not finish, in words. */
export const testErrorText = (e: unknown): string => TEST_ERRORS[codeOf(e)] ?? errorText(e);

/** The scheme shown on the schemes page. Its screen id changes when it is first stored; the selection follows. */
const selected = () => { const s = getState(); return s.presets.find((p) => p.id === s.presetSel) ?? s.presets[0]; };

/** Detects what the selected scheme finds in text. Rejects with a closed code. */
export async function runPresetTest(text: string): Promise<TestResult> {
  const e = liveEngine();
  if (e) {
    await flushConfig(); // the test runs the scheme as stored: unsaved edits are written first
    const stored = selected()?.stored?.id;
    if (!stored) throw new Error("NOT_FOUND");
    return testFromEngine(await e.testPreset(stored, text));
  }
  const s = getState();
  const p = selected();
  if (!p) throw new Error("NOT_FOUND");
  if (p.det === "model" && s.models.find((m) => m.id === p.model)?.status !== "ready") throw new Error("MODEL_NOT_INSTALLED");
  return simulateTest(p, availableRulesets(s.rulesets, s.plugins), s.models, text);
}
