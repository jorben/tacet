import { useRef, useState } from "react";
import { installSamplePlugin, uninstallPlugin } from "../../actions/plugins";
import { Modal, ModalBody, ModalFooter, ModalHeader } from "../../overlay/Modal";
import { closeModal } from "../../overlay/store";
import { useAppState } from "../../store";
import { Button, Field, Icon, KeyValue, Segmented } from "../../ui";

const DEFAULT_SOURCE = "https://github.com/mailtools/tacet-eml";

type Source = "git" | "dir";

/** Choosing where a plugin comes from. */
function SourceStep({ onNext }: { onNext: (source: string) => void }) {
  const [kind, setKind] = useState<Source>("git");
  const [folder, setFolder] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);
  return (
    <Modal>
      <ModalHeader title="安装插件" />
      <ModalBody>
        <Segmented full value={kind} onChange={setKind} options={[{ value: "git", label: "Git 仓库" }, { value: "dir", label: "本地文件夹" }]} />
        {kind === "dir" ? (
          <button type="button" className="field" style={{ width: "100%" }} onClick={() => setFolder("~/plugins/tacet-eml")}>
            <Icon name="folder" size={15} />
            <span className={folder ? undefined : "muted"} id="dirv">
              {folder ?? "选择文件夹…"}
            </span>
          </button>
        ) : (
          <Field ref={input} icon="git" id="instSrc" placeholder="https://github.com/…" defaultValue={DEFAULT_SOURCE} />
        )}
      </ModalBody>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" onClick={() => onNext(kind === "git" ? input.current?.value ?? DEFAULT_SOURCE : "local/tacet-eml")}>
          下一步
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** What the plugin will be allowed to do, before it is installed. */
function ReviewStep({ source }: { source: string }) {
  return (
    <Modal>
      <div style={{ display: "flex", gap: 12, alignItems: "center", marginBottom: 4 }}>
        <span className="ficon">
          <Icon name="file" size={17} />
        </span>
        <div>
          <h3 style={{ margin: 0 }}>EML 邮件</h3>
          <div className="ms">格式插件 · 0.2.1</div>
        </div>
      </div>
      <ModalBody>
        <KeyValue
          items={[
            ["来源", source.replace(/^https?:\/\//, ""), "mono"],
            ["版本", "提交 8c41e07 · 固定", "mono"],
            ["格式", ".eml · 读取 · 回写 · 验证"],
            ["权限", <>仅指定输入与临时区 · <b>无网络</b></>],
            ["资源", "内存 ≤ 512 MiB"],
            ["许可", "MIT（作者声明）· 依赖 9"],
          ]}
        />
      </ModalBody>
      <div className="warnline" style={{ justifyContent: "flex-start" }}>
        <Icon name="alert" size={14} />
        插件可读取文档原文
      </div>
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          取消
        </Button>
        <Button variant="primary" onClick={installSamplePlugin}>
          安装
        </Button>
      </ModalFooter>
    </Modal>
  );
}

/** Install a plugin (a demonstration: it always installs the same sample). */
export function InstallPluginDialog() {
  const [source, setSource] = useState<string | null>(null);
  return source === null ? <SourceStep onNext={setSource} /> : <ReviewStep source={source} />;
}

/** Removing a plugin that recoverable tasks still use invalidates those tasks. */
export function UninstallPluginDialog({ id }: { id: string }) {
  const plugin = useAppState((s) => s.plugins.find((p) => p.id === id));
  if (!plugin) return null;
  return (
    <Modal>
      <ModalHeader title={"卸载 " + plugin.name} subtitle={`有 ${plugin.refs} 个可恢复任务在使用此版本`} />
      <ModalFooter>
        <Button variant="ghost" onClick={closeModal}>
          等待
        </Button>
        <Button variant="danger" onClick={() => uninstallPlugin(plugin.id)}>
          作废任务并卸载
        </Button>
      </ModalFooter>
    </Modal>
  );
}
