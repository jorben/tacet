import type { ReactNode } from "react";
import { Button, Icon, IconButton, Tag, tip } from "../../ui";

interface ConfigHeaderProps {
  name: string;
  version: string;
  /** Built-in or plugin-provided: cannot be edited, only copied. */
  locked: boolean;
  lockedHint: string;
  /** Line under the name. */
  subtitle?: ReactNode;
  onCopy: () => void;
  onMore: (anchor: HTMLElement) => void;
}

/** Name, version and actions of the scheme or rule set being shown. */
export function ConfigHeader({ name, version, locked, lockedHint, subtitle, onCopy, onMore }: ConfigHeaderProps) {
  return (
    <div className="phead">
      {subtitle != null ? (
        <div style={{ minWidth: 0 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <div className="h1">{name}</div>
            <Tag primary={!locked}>{version}</Tag>
          </div>
          <div className="muted sm" style={{ marginTop: 4 }}>
            {subtitle}
          </div>
        </div>
      ) : (
        <>
          <div className="h1">{name}</div>
          <Tag primary={!locked}>{version}</Tag>
        </>
      )}
      <span className="sp" />
      {locked && (
        <span className="ib" tabIndex={0} {...tip(lockedHint, { align: "end" })}>
          <Icon name="lock" size={16} />
        </span>
      )}
      <Button icon="copy" onClick={onCopy}>
        复制
      </Button>
      <IconButton icon="more" label="更多" onClick={(e) => onMore(e.currentTarget)} />
    </div>
  );
}
