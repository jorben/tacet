// Plugins: enabling, installing (simulated) and removing.
import { getState, store } from "../store";
import { closeModal, openMenu, openModal, toast, type PopAnchor } from "../overlay/store";
import { nextId } from "./timers";
import { isLive } from "./live";

export function togglePlugin(id: string): void {
  if (isLive()) { toast("内建插件是处理文档所必需的，不能停用"); return; }
  let on = false;
  store.update((d) => { const p = d.plugins.find((x) => x.id === id)!; p.on = !p.on; on = p.on; });
  toast(on ? "已启用" : "已停用 · 进行中的任务不受影响");
}

/** Installing is a demonstration: it always adds the same sample plugin and makes no request. */
export function installSamplePlugin(): void {
  closeModal();
  if (isLive()) { toast("第三方插件需要系统沙箱验收后才能安装，当前版本尚不可用"); return; }
  store.update((d) => {
    const at = d.plugins.findIndex((p) => p.builtin);
    d.plugins.splice(at, 0, { id: nextId("eml"), name: "EML 邮件", role: "格式", fmt: ".eml", ver: "0.2.1", src: "github.com/mailtools/tacet-eml", commit: "8c41e07", on: true, trusted: false, refs: 0 });
    d.plugins.sort((a, b) => (a.builtin ? 1 : 0) - (b.builtin ? 1 : 0));
  });
  toast("已安装 EML 邮件");
}

export function openPluginMenu(anchor: PopAnchor, id: string): void {
  if (isLive()) { toast("第三方插件当前版本尚不可用"); return; }
  const p = getState().plugins.find((x) => x.id === id)!;
  openMenu(anchor, [
    { l: p.trusted ? "取消完成判定授权" : "授权完成判定", s: "当前版本", ic: "shield", k: "trust", on: p.trusted },
    { l: "升级…", ic: "download", k: "up" },
    { l: "回退版本", ic: "undo", k: "down" },
    "-",
    { l: "卸载", ic: "trash", k: "rm", d: true },
  ], (it) => {
    if (it.k === "trust") {
      store.update((d) => { const x = d.plugins.find((q) => q.id === id)!; x.trusted = !x.trusted; });
      toast(!p.trusted ? "结果可标为“已按策略完成”" : "已取消授权");
    } else if (it.k === "rm") {
      if (p.refs) openModal({ type: "uninstallPlugin", id });
      else { store.update((d) => { d.plugins = d.plugins.filter((x) => x.id !== id); }); toast("已卸载"); }
    } else toast(it.k === "up" ? "没有新版本" : "已是最早版本");
  }, { alignRight: true });
}

export function uninstallPlugin(id: string): void {
  store.update((d) => { d.plugins = d.plugins.filter((x) => x.id !== id); });
  closeModal();
  toast("已卸载 · 已导出的文件不受影响");
}
