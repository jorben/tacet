import { useRef } from "react";
import { publishValidated, resolveException } from "../../actions/batch";
import { enterBatchReview } from "../../actions/review";
import { countFiles, groupMembers, groupTotal } from "../../domain/batch";
import { fmt } from "../../domain/format";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal } from "../../overlay/store";
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
