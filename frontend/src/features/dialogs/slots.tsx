import { parkAndContinue, viewSlotTask } from "../../actions/home";
import { batchStatus, keepDays } from "../../domain/batch";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal } from "../../overlay/store";
import { useAppState } from "../../store";
import { slotBusy } from "../../store/selectors";
import { Button, pushLeft } from "../../ui";

/** The slot is taken by an unfinished task: keep it for later, go back to it, or cancel. */
export function SlotConflictDialog({ kind, go }: { kind: "single" | "batch"; go: () => void }) {
  const state = useAppState((s) => s);
  if (kind === "single") {
    const cur = slotBusy(state, "single");
    if (!cur) return null;
    return (
      <Modal>
        <ModalHeader title={`「${cur.name}」还未导出`} subtitle="保存到“最近”，稍后可继续确认" />
        <ModalFooter>
          <Button variant="ghost" style={pushLeft} onClick={closeModal}>
            取消
          </Button>
          <Button onClick={() => viewSlotTask("single")}>返回该文件</Button>
          <Button variant="primary" onClick={() => parkAndContinue("single", go)}>
            保存并继续
          </Button>
        </ModalFooter>
      </Modal>
    );
  }
  const cur = slotBusy(state, "batch");
  if (!cur) return null;
  const precheck = cur.phase === "precheck";
  return (
    <Modal>
      <ModalHeader title={precheck ? "当前批次还未开始" : "当前批次还在进行"} subtitle={`「${cur.title}」· ${batchStatus(cur)[1]}`} />
      <ModalBody>{precheck ? "替换后，当前批次的设置不会保留。" : `当前批次将暂停并加密保存 ${keepDays(cur)} 天，可在“最近”中继续。`}</ModalBody>
      <ModalFooter>
        <Button variant="ghost" style={pushLeft} onClick={closeModal}>
          取消
        </Button>
        <Button onClick={() => viewSlotTask("batch")}>查看当前批次</Button>
        <Button variant="primary" onClick={() => parkAndContinue("batch", go)}>
          {precheck ? "替换" : "保存并继续"}
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** Changing the scheme of a document that has edits throws them away. */
export function SwitchPresetDialog({ preset, go }: { preset: string; go: () => void }) {
  return (
    <Modal>
      <ModalHeader title={`切换到「${preset}」`} subtitle="将按新方案重新检测，你在此文件上的修改会失效" />
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" onClick={go}>
          切换并重新检测
        </Button>
      </ModalFooter>
    </Modal>
  );
}
