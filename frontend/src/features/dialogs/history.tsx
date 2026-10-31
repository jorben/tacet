import { discardSavedTask, resumeSavedBatch } from "../../actions/home";
import { revealOutput } from "../../actions/single";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, KeyValue, pushLeft } from "../../ui";
import { HistoryRows } from "../home/HistoryRows";
import { useLiveHistory } from "../home/useLiveHistory";

/** Every recent task, not only the first three shown on the home page. */
export function RecentTasksDialog() {
  const history = useLiveHistory();
  return (
    <Modal className="history-modal" labelledBy="historyTitle" initialFocus=".hrow,.btn">
      <ModalHeader title="最近任务" titleId="historyTitle" subtitle={history.length + " 个任务"} />
      <div className="list home-history">
        <HistoryRows entries={history} />
      </div>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          关闭
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** Details of a finished task that has no result page of its own. */
export function HistoryItemDialog({ entryId }: { entryId: string }) {
  const entry = useAppState((s) => s.history.find((h) => h.id === entryId));
  if (!entry) return null;
  return (
    <Modal>
      <ModalHeader title={entry.name} subtitle={entry.meta + " · " + entry.when} />
      <ModalBody>
        <KeyValue items={[["状态", entry.stl], ["位置", "桌面 / 已脱敏"]]} />
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          关闭
        </Button>
        <Button variant="primary" icon="folder" onClick={revealOutput}>
          打开导出文件夹
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** A batch that was saved for later: continue it or throw it away. */
export function ResumeBatchDialog({ entryId }: { entryId: string }) {
  const entry = useAppState((s) => s.history.find((h) => h.id === entryId));
  if (!entry) return null;
  return (
    <Modal>
      <ModalHeader title={entry.name} subtitle={entry.meta + " · " + entry.stl} />
      <ModalBody>继续后从中断处处理，已提交的决定保留。</ModalBody>
      <ModalFooter>
        <Button variant="danger" style={pushLeft} onClick={() => discardSavedTask(entry.id)}>
          停止并清理
        </Button>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" onClick={() => resumeSavedBatch(entry.id)}>
          继续处理
        </Button>
      </ModalFooter>
    </Modal>
  );
}
