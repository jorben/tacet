import { useEffect, useState } from "react";
import { Icon } from "../ui";
import "../styles/dev-navigator.css";
import { scene, SCENE_GROUPS } from "./scenes";

let deepLinkApplied = false;

/** A floating menu to jump to any screen state; `#scene=<key>` in the URL does the same on load. */
export default function SceneNavigator() {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (deepLinkApplied) return;
    deepLinkApplied = true;
    const key = new URLSearchParams(location.hash.slice(1)).get("scene");
    if (key) scene(key);
  }, []);
  useEffect(() => {
    const close = (e: MouseEvent) => {
      if (!(e.target as Element).closest("#proto")) setOpen(false);
    };
    document.addEventListener("click", close);
    return () => document.removeEventListener("click", close);
  }, []);
  return (
    <div id="proto" className={open ? "open" : undefined} onClick={(e) => e.stopPropagation()}>
      <div className="pm">
        <div className="h">交互演示 · 下载与安装均为模拟</div>
        {SCENE_GROUPS.map(([heading, items]) => (
          <div key={heading}>
            <div className="h">{heading}</div>
            {items.map(([label, key]) => (
              <button key={key} type="button" data-scene={key} onClick={() => { setOpen(false); scene(key); }}>
                {label}
              </button>
            ))}
          </div>
        ))}
      </div>
      <button type="button" aria-label="原型场景" onClick={() => setOpen((o) => !o)}>
        原型场景 <Icon name="chev-d" size={12} />
      </button>
    </div>
  );
}
