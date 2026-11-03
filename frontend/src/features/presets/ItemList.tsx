import { useEffect, useRef } from "react";
import { Icon, IconButton, IconTile, tip } from "../../ui";

export interface ListItem {
  id: string;
  icon: string;
  title: string;
  /** One line: the version and one key figure. */
  subtitle: string;
  /** Built-in or from a plugin: shown, copied, not edited. */
  locked?: boolean;
}

interface ItemListProps {
  /** What the column lists (“方案”), its heading. */
  label: string;
  items: ListItem[];
  /** Absent while there is nothing to select. */
  selectedId?: string;
  onSelect: (id: string) => void;
  /** Absent: the list offers no “新建” / “导入” (e.g. models, which come with the application). */
  onCreate?: () => void;
  onImport?: () => void;
}

/**
 * The column of schemes, rule sets or models next to the editor. “新建” and “导入” sit in its heading, which stays
 * put while the list scrolls; the selected item is scrolled into view.
 */
export function ItemList({ label, items, selectedId, onSelect, onCreate, onImport }: ItemListProps) {
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>("[aria-current=true]")?.scrollIntoView?.({ block: "nearest" });
  }, [selectedId]);
  return (
    <div className="preset-sidebar">
      <div className="plist-head">
        <span>{label}</span>
        <span className="sp" />
        {onCreate && <IconButton icon="plus" size={16} label="新建" tooltip={"新建" + label} onClick={onCreate} />}
        {onImport && <IconButton icon="folder" size={16} label="导入" tooltip={"导入" + label} tooltipAlign="end" onClick={onImport} />}
      </div>
      <div className="plist" ref={list}>
        {items.map((x) => (
          <button key={x.id} type="button" className="pitem" aria-current={x.id === selectedId} onClick={() => onSelect(x.id)}>
            <IconTile icon={x.icon} size={30} iconSize={15} />
            <span className="pitem-text">
              <span className="pn">
                <span className="pn-t" title={x.title}>{x.title}</span>
                {x.locked && <span className="pn-lock" aria-label="只读" {...tip("只读，复制后可编辑")}><Icon name="lock" size={12} /></span>}
              </span>
              <span className="pv" title={x.subtitle}>{x.subtitle}</span>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
