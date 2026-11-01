import { useRef, useState } from "react";
import { retryLive, skipLive, unlockLive } from "../../actions/live";
import { publishValidated, resolveException } from "../../actions/batch";
import { enterBatchReview } from "../../actions/review";
import { countFiles, groupMembers, groupTotal } from "../../domain/batch";
import { fmt } from "../../domain/format";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal, toast } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Field, KeyValue, pushLeft } from "../../ui";

/** Confirms a bulk “keep” or “skip” on a whole group: both leave findings in the output. */
export function BulkConfirmDialog({ gid, k }: { gid: string; k: string }) {
  const batch = useAppState((s) => s.batch);
  const g = batch?.groups.find((x) => x.id === gid);
  const act = g?.acts.find((a) => a.k === k);
  if (!batch || !g || !act) return null;
  const members = groupMembers(batch, g);
  const n = groupTotal(g, members);
  const skip = k === "skip";
  return (
    <Modal>
      <ModalHeader title={`全部${act.l} · ${fmt(n)} ${g.unit}`} subtitle={`${g.t} · 涉及 ${fmt(members.length)} 份文件`} />
      <ModalBody>
        {skip ? "这些文件将不导出，计入未导出范围。" : g.gap ? `未检查的内容将原样保留，这 ${fmt(members.length)} 份将导出为部分结果。` : "这些内容将原样保留在导出文件中。"}
        <div className="muted sm" style={{ marginTop: 8 }}>
          建议先在复核中查看样例
        </div>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" style={pushLeft} onClick={closeModal}>
          取消
        </Button>
        <Button
          onClick={() => {
            closeModal();
            enterBatchReview(gid);
          }}
        >
          先去复核
        </Button>
        <Button
          variant={skip ? "danger" : "primary"}
          onClick={() => {
            closeModal();
            resolveException(gid, k);
          }}
        >
          全部{act.l}
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** Asks for the password of each locked file in a group. Passwords are used once and not kept. */
export function PasswordDialog({ gid, members }: { gid: string; members: number[] | undefined }) {
  const batch = useAppState((s) => s.batch);
  // The members are fixed when the dialog opens, so a file unlocked in the meantime does not disappear from it.
  const fixed = useRef(members);
  const g = batch?.groups.find((x) => x.id === gid);
  if (!batch || !g) return null;
  fixed.current ||= groupMembers(batch, g);
  const mem = fixed.current;
  return (
    <Modal>
      <ModalHeader title="输入密码" subtitle="仅本次使用，不会保存" />
      <ModalBody>
        {mem.map((fi) => (
          <div key={fi}>
            <div className="sm" style={{ marginTop: 12, color: "var(--ink-2)" }}>
              {batch.files[fi]!.name}
            </div>
            <Field icon="lock" type="password" aria-label="密码" />
          </div>
        ))}
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button
          variant="primary"
          onClick={() => {
            closeModal();
            resolveException(gid, "pwd", mem);
          }}
        >
          解锁
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** Files that passed validation wait for the user's go-ahead before they are written out. */
export function ExportReadyDialog() {
  const batch = useAppState((s) => s.batch);
  if (!batch) return null;
  const c = countFiles(batch.files);
  const manual = batch.files.filter((f) => f.st === "ready" && f.man).length;
  const partial = batch.files.filter((f) => f.st === "ready" && f.partial).length;
  return (
    <Modal>
      <ModalHeader title={`导出 ${fmt(c.ready)} 份`} subtitle={"到 " + batch.out} />
      <ModalBody>
        <KeyValue
          items={[
            ["自动", fmt(c.ready - manual) + " 份"],
            ["含人工决定", fmt(manual) + " 份"],
            ["部分结果", fmt(partial) + " 份" + (partial ? " · 文件名带标记" : "")],
            ["仍未导出", fmt(c.wait + c.held + c.bad + c.queue + c.proc) + " 份"],
          ]}
        />
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" onClick={publishValidated}>
          导出
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** Asks for the password of each locked file. A password is handed to the kernel once and forgotten here. */
export function LivePasswordDialog({ targets: initial }: { targets: { job: string; name: string }[] }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [targets, setTargets] = useState(initial);
  // only files of the batch can be skipped; a quick task is stopped from its own window instead
  const inBatch = useAppState((s) => s.batch?.live?.jobs) ?? [];
  const skip = async (job: string) => {
    if (!(await skipLive(job))) return;
    const rest = targets.filter((t) => t.job !== job);
    setValues(({ [job]: _, ...v }) => v);
    if (!rest.length) closeModal();
    else setTargets(rest);
  };
  const submit = async () => {
    setBusy(true);
    const entries = Object.entries(values).filter(([, v]) => v);
    setValues({});
    for (const [job, pw] of entries) await unlockLive(job, pw);
    setBusy(false);
    closeModal();
  };
  return (
    <Modal>
      <ModalHeader title="输入密码" subtitle="仅本次使用，不会保存" />
      <ModalBody>
        {targets.map((t) => (
          <div key={t.job}>
            <div className="sm" style={{ marginTop: 12, color: "var(--ink-2)", display: "flex", alignItems: "center" }}>
              {t.name}
              {inBatch.includes(t.job) && (
                <Button variant="ghost" style={{ marginLeft: "auto" }} disabled={busy} onClick={() => void skip(t.job)}>
                  跳过此文件
                </Button>
              )}
            </div>
            <Field
              icon="lock"
              type="password"
              aria-label={"密码 " + t.name}
              autoComplete="off"
              value={values[t.job] ?? ""}
              onChange={(e) => setValues((v) => ({ ...v, [t.job]: e.target.value }))}
            />
          </div>
        ))}
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" disabled={busy || !Object.values(values).some(Boolean)} onClick={() => void submit()}>
          解锁
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** The files of a batch whose processing is blocked: process them again, or leave one out (requirements 2.3). */
export function LiveBlockedDialog({ targets: initial }: { targets: { job: string; name: string; reason: string }[] }) {
  const [targets, setTargets] = useState(initial);
  const [busy, setBusy] = useState(false);
  const skip = async (job: string) => {
    setBusy(true);
    const ok = await skipLive(job);
    setBusy(false);
    if (!ok) return;
    const rest = targets.filter((t) => t.job !== job);
    if (!rest.length) closeModal();
    else setTargets(rest);
  };
  const retry = async () => {
    setBusy(true);
    await retryLive();
    setBusy(false);
    closeModal();
    toast("已重新排队");
  };
  return (
    <Modal>
      <ModalHeader title="处理受阻" subtitle="重新处理，或跳过不导出" />
      <ModalBody>
        {targets.map((t) => (
          <div key={t.job} className="sm" style={{ marginTop: 12, display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ color: "var(--ink-2)" }}>{t.name}</span>
            <span className="muted">{t.reason}</span>
            <Button variant="ghost" style={{ marginLeft: "auto" }} disabled={busy} onClick={() => void skip(t.job)}>
              跳过此文件
            </Button>
          </div>
        ))}
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" disabled={busy} onClick={() => void retry()}>
          重新处理
        </Button>
      </ModalFooter>
    </Modal>
  );
}
