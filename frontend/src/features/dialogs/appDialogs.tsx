import { Fragment, useSyncExternalStore } from "react";
import { onRefusedChange, refusedJobs } from "../../actions/decisions";
import { openDecisions, savedBatchId } from "../../actions/live";
import { cleanWorkspace, confirmUpdate } from "../../actions/settings";
import { saveAndQuit, stopAndQuit } from "../../actions/window";
import { batchStatus } from "../../domain/batch";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal } from "../../overlay/store";
import { useAppState, type AppState } from "../../store";
import { isRecoverable, refusedFiles, screenSingle, unfinishedBatch, unfinishedOnScreen, type RefusedFile } from "../../store/selectors";
import { Button, ButtonLink, Icon, KeyValue, pushLeft } from "../../ui";

const ABOUT_LABEL = { website: "官网", discord: "Discord 社区", github: "GitHub" } as const;

/** Unfinished work, one line each. `includeSaved` also lists tasks that were saved for later. */
function unfinishedTasks(s: AppState, includeSaved: "parked" | "all"): string[] {
  const out: string[] = [];
  const single = unfinishedOnScreen(s, "single");
  const batch = unfinishedBatch(s);
  if (single) out.push(single.name + " · " + (single.phase === "review" ? "待确认" : "处理中"));
  if (batch) out.push(batch.title + " · " + batchStatus(batch)[1]);
  s.history.filter((h) => (includeSaved === "all" ? isRecoverable(h) : h.park)).forEach((h) => out.push(h.name + " · " + h.stl));
  return out;
}

const TaskLine = ({ children }: { children: string }) => (
  <div className="sm" style={{ padding: "3px 0", color: "var(--ink-2)" }}>
    {children}
  </div>
);

/** Installing an update (a demonstration) with a say in what happens to unfinished tasks. */
export function UpdateAppDialog() {
  const latest = useAppState((s) => s.update.latest);
  const tasks = useAppState((s) => unfinishedTasks(s, "all"), (a, b) => a.join("|") === b.join("|"));
  if (!tasks.length) {
    return (
      <Modal>
        <ModalHeader title={`更新至 ${latest}`} subtitle="更新功能尚未接入" />
        <ModalBody>
          <p>更新包校验签名后安装，完成后重新打开 Tacet。当前没有未完成的任务。</p>
        </ModalBody>
        <ModalFooter>
          <Button variant="ghost" onClick={closeModal}>
            取消
          </Button>
          <Button variant="primary" onClick={() => confirmUpdate()}>
            更新
          </Button>
        </ModalFooter>
      </Modal>
    );
  }
  return (
    <Modal>
      <ModalHeader title={`更新至 ${latest}`} subtitle={`${tasks.length} 个任务未完成 · 更新功能尚未接入`} />
      <ModalBody>
        {tasks.map((t, i) => (
          <TaskLine key={i}>{t}</TaskLine>
        ))}
        <p className="muted sm" style={{ marginTop: 12 }}>
          更新会替换检测模型、规则与格式插件。可以先完成这些任务；也可以作废它们，或在更新后按新版本重新执行受影响的步骤，已提交的决定核对一致后保留。已导出的文件不受影响。
        </p>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" style={pushLeft} onClick={closeModal}>
          先完成
        </Button>
        <Button variant="danger" onClick={() => confirmUpdate("drop")}>
          作废并更新
        </Button>
        <Button variant="primary" onClick={() => confirmUpdate("rerun")}>
          更新后重新执行
        </Button>
      </ModalFooter>
    </Modal>
  );
}

export function AboutLinkDialog({ which }: { which: keyof typeof ABOUT_LABEL }) {
  const label = ABOUT_LABEL[which];
  return (
    <Modal>
      <ModalHeader title={`Tacet ${label}`} subtitle="地址待配置" />
      <ModalBody>
        <p>配置正式{label}地址后，将在浏览器中打开。</p>
      </ModalBody>
      <ModalFooter>
        <Button variant="primary" onClick={closeModal}>
          知道了
        </Button>
      </ModalFooter>
    </Modal>
  );
}

export function LicensesDialog() {
  return (
    <Modal>
      <ModalHeader title="开源许可" subtitle="Tacet · Apache License 2.0" />
      <ModalBody>
        <KeyValue items={[["应用主体", "Apache-2.0"], ["插件 SDK", "Apache-2.0"], ["协议 Schema", "Apache-2.0"]]} />
        <p style={{ marginTop: 16 }}>第三方插件及依赖组件遵循各自的许可，具体声明以安装包中的许可证清单为准。</p>
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          关闭
        </Button>
        <ButtonLink style={{ textDecoration: "none" }} href="https://www.apache.org/licenses/LICENSE-2.0">
          查看许可原文 <Icon name="ext" size={14} />
        </ButtonLink>
      </ModalFooter>
    </Modal>
  );
}

export function CleanWorkspaceDialog() {
  const history = useAppState((s) => s.history);
  const saved = history.filter(isRecoverable);
  return (
    <Modal>
      <ModalHeader title="清理工作区" subtitle="删除中间数据与恢复数据，已导出的文件不受影响" />
      <ModalBody>
        <KeyValue
          items={[
            ["可恢复", saved.length ? saved.map((h, i) => (
              <Fragment key={h.id}>
                {i > 0 && <br />}
                {h.name} · {h.meta}
              </Fragment>
            )) : "无"],
            ["占用", "1.4 GiB"],
          ]}
        />
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="danger" onClick={cleanWorkspace}>
          清理
        </Button>
      </ModalFooter>
    </Modal>
  );
}

const sameFiles = (a: RefusedFile[], b: RefusedFile[]) => a.map((f) => f.job).join("|") === b.map((f) => f.job).join("|");

/** The tasks the dialog lists as kept: all but a single task that goes as a whole. */
function tasksLeft(s: AppState, files: RefusedFile[]): string[] {
  const whole = new Set(files.filter((f) => !f.inBatch).map((f) => f.batch));
  const single = screenSingle(s)?.live?.batch;
  return unfinishedTasks({ ...s, single: single && whole.has(single) ? null : s.single, behind: single && whole.has(single) ? null : s.behind,
    history: s.history.filter((h) => !whole.has(savedBatchId(h.saved) ?? "")) }, "parked");
}

/** Closing the window with unfinished work: keep it, or stop and clean up. With choices the kernel refused, cleaning
 *  reaches those files only, and the dialog says which while it is open: a write can still fail. */
export function CloseWindowDialog() {
  const jobs = useSyncExternalStore(onRefusedChange, refusedJobs);
  const files = useAppState((s) => refusedFiles(s, jobs), sameFiles);
  const tasks = useAppState((s) => (files.length ? tasksLeft(s, files) : unfinishedTasks(s, "parked")), (a, b) => a.join("|") === b.join("|"));
  const batchKeep = useAppState((s) => unfinishedBatch(s)?.keep);
  const settingsKeep = useAppState((s) => s.settings.keep);
  const days = batchKeep && batchKeep !== "0" ? batchKeep : settingsKeep !== "0" ? settingsKeep : "7";
  // choices the kernel can no longer take: the person's last word on them, whether a toast was seen or not
  const lost = useAppState((s) => openDecisions(s).filter((o) => o.closed).map((o) => o.name), (a, b) => a.join("|") === b.join("|"));
  const onlyChoices = !files.length && !tasks.length;
  const subtitle = onlyChoices
    ? (lost.length ? "关闭后这些修改不会保留；已导出的文件不受影响" : "“保存并关闭”会把还没保存的选择交给内核；已导出的文件不受影响")
    : files.length
    ? `${files.length} 个文件的选择未能保存。“保存并关闭”会再试一次；“停止并清理”只清理这些文件的中间数据，其他任务保留 ${days} 天，下次打开继续；已导出的文件不受影响`
    : `保存后加密保留 ${days} 天，下次打开继续；停止将清理下列全部任务的中间数据，已导出的文件不受影响`;
  return (
    <Modal>
      <ModalHeader title={onlyChoices ? "有未保存的选择" : "有未完成的任务"} subtitle={subtitle} />
      <ModalBody>
        {lost.length > 0 && <p className="muted sm">已开始导出或已导出，这里的修改不会进入导出文件：</p>}
        {lost.map((name, i) => (
          <TaskLine key={i}>{name}</TaskLine>
        ))}
        {files.length > 0 && <p className="muted sm">“停止并清理”将清理：</p>}
        {files.map((f) => (
          <TaskLine key={f.job}>{f.inBatch ? f.name + " · " + f.inBatch + "中的文件" : f.name}</TaskLine>
        ))}
        {files.length > 0 && tasks.length > 0 && <p className="muted sm" style={{ marginTop: 12 }}>保留：</p>}
        {tasks.map((t, i) => (
          <TaskLine key={i}>{t}</TaskLine>
        ))}
      </ModalBody>
      <ModalFooter spaced>
        <Button variant="danger" style={pushLeft} onClick={stopAndQuit}>
          停止并清理
        </Button>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" onClick={saveAndQuit}>
          保存并关闭
        </Button>
      </ModalFooter>
    </Modal>
  );
}
