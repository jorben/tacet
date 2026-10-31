import { useEffect, useRef, useState } from "react";
import { openSampleDrawer, openBatchFile } from "../../actions/batch";
import { CATEGORIES } from "../../domain/catalog";
import { randInt } from "../../domain/random";
import type { CategoryId } from "../../domain/types";
import { Drawer } from "../../overlay/Drawer";
import { closeModal, type SampleRow } from "../../overlay/store";
import { Button, Icon, IconButton, Swatch } from "../../ui";

const CONTEXT: Record<CategoryId | "img", string> = {
  org: "单位：", name: "联系人：", phone: "电话：", email: "邮箱：", id: "身份证号：", addr: "住址：", card: "账户：", date: "出生日期：", img: "", other: "",
};

/** A masked value that shows its digits for a moment when clicked. */
function PeekValue({ value }: { value: string }) {
  const [shown, setShown] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => () => clearTimeout(timer.current), []);
  const peek = () => {
    setShown(value.replace(/•+/g, (m) => Array.from(m, () => String(randInt(0, 9))).join("")));
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setShown(null), 2500);
  };
  return (
    <span className="mask" role="button" tabIndex={0} onClick={peek} onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); peek(); } }}>
      <Icon name="eye" size={12} />
      {shown ?? value}
    </span>
  );
}

/** Eight random findings of one category across files, for spot-checking the automatic work. */
export function SampleDrawer({ category, rows }: { category: CategoryId | "img"; rows: SampleRow[] }) {
  const image = category === "img";
  return (
    <Drawer>
      <div className="dh">
        <div className="gi" style={{ background: "var(--surface-2)", color: "var(--ink-2)" }}>
          {image ? <Icon name="image" size={17} /> : <Swatch category={category} size={12} />}
        </div>
        <div>
          <div className="t">抽查 · {image ? "图像" : CATEGORIES[category].l}</div>
          <div className="sm muted">随机 8 处 · 跨文件</div>
        </div>
        <span className="sp" />
        <IconButton icon="retry" size={16} label="换一批" tooltip="换一批" onClick={() => openSampleDrawer(category, true)} />
        <IconButton icon="x" label="关闭" onClick={closeModal} />
      </div>
      <div className="dl">
        {rows.map((r, j) => (
          <div className="drow" style={{ cursor: "default" }} key={j}>
            {image && (
              <span className="dthumb">
                <i></i>
              </span>
            )}
            <span className="dx">
              <div className="dn">{r.fileName}</div>
              <div className="dc">
                {r.location} · {CONTEXT[category]}
                <PeekValue value={r.value} />
              </div>
            </span>
            <Button variant="ghost" onClick={() => openBatchFile(r.fileIndex)}>
              查看
            </Button>
          </div>
        ))}
      </div>
      <div className="df">
        <span className="muted sm">抽查不代表逐项审阅 · 误判请打开文件修改</span>
        <span className="sp" />
        <Button onClick={closeModal}>完成</Button>
      </div>
    </Drawer>
  );
}
