import type { ReactNode } from "react";
import { IconTile } from "../../ui";

export interface ListItem {
  id: string;
  icon: string;
  title: ReactNode;
  subtitle: ReactNode;
}

interface ItemListProps {
  items: ListItem[];
  selectedId: string;
  onSelect: (id: string) => void;
  onCreate: () => void;
  onImport: () => void;
}

/** The column of schemes or rule sets next to the editor, with “新建” and “导入” at the end. */
export function ItemList({ items, selectedId, onSelect, onCreate, onImport }: ItemListProps) {
  return (
    <div className="preset-sidebar">
      <div className="plist">
        {items.map((x) => (
          <button key={x.id} type="button" className="pitem" aria-current={x.id === selectedId} onClick={() => onSelect(x.id)}>
            <IconTile icon={x.icon} size={30} iconSize={15} />
            <span>
              <div className="pn">{x.title}</div>
              <div className="pv">{x.subtitle}</div>
            </span>
          </button>
        ))}
        <button type="button" className="pitem" style={{ color: "var(--muted)" }} onClick={onCreate}>
          <IconTile icon="plus" size={30} iconSize={15} outline />
          新建
        </button>
        <button type="button" className="pitem" style={{ color: "var(--muted)" }} onClick={onImport}>
          <IconTile icon="folder" size={30} iconSize={15} outline />
          导入
        </button>
      </div>
    </div>
  );
}
