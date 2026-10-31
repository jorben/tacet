import { Icon } from "./Icon";

/** A document glyph with its format as a badge. Archives show a folder. */
export function FileIcon({ ext }: { ext: string }) {
  return (
    <span className="ficon file">
      <Icon name={ext === "zip" ? "folder" : "file"} size={17} />
      <b>{(ext || "").toUpperCase().slice(0, 4)}</b>
    </span>
  );
}

/** A square tile holding an icon, used as the lead of list items. */
export function IconTile({ icon, size = 34, iconSize = 16, outline }: { icon: string; size?: number; iconSize?: number; outline?: boolean }) {
  return (
    <span
      className="ficon"
      style={{ width: size, height: size, ...(outline ? { background: "none", boxShadow: "inset 0 0 0 1px var(--line)" } : {}) }}
    >
      <Icon name={icon} size={iconSize} />
    </span>
  );
}
