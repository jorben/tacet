import { Fragment, type ReactNode } from "react";
import { Icon, LinkButton, Tag } from "../../ui";

interface ConfigHeaderProps {
  name: string;
  /** The tag after the name: a version, or a model's mapping state. */
  badge: string;
  badgePrimary?: boolean;
  /** The second line: what the object uses or is used by. Always there, so the head keeps one height on every tab. */
  subtitle: ReactNode;
  /** Actions on the right. */
  children: ReactNode;
  /** Why the object cannot be edited, under the head. */
  notice?: ReactNode;
}

/** Name, tag, one line of references and the actions of the scheme, rule set or model being shown. */
export function ConfigHeader({ name, badge, badgePrimary, subtitle, children, notice }: ConfigHeaderProps) {
  return (
    <>
      <div className="phead">
        <div className="phead-text">
          <div className="phead-title">
            <div className="h1" title={name}>{name}</div>
            <Tag primary={badgePrimary}>{badge}</Tag>
          </div>
          <div className="muted sm phead-sub">{subtitle}</div>
        </div>
        <span className="sp" />
        {children}
      </div>
      {notice}
    </>
  );
}

/** “内建方案，只读” with the way to an editable copy. */
export function ReadonlyNotice({ children, onCopy }: { children: ReactNode; onCopy: () => void }) {
  return (
    <div className="config-note" role="note">
      <Icon name="lock" size={14} />
      <span>{children}</span>
      <LinkButton onClick={onCopy}>复制后编辑</LinkButton>
    </div>
  );
}

/** Names of other objects, each a link to it; “、” between them. */
export function RefLinks({ items, onOpen }: { items: { id: string; name: string }[]; onOpen: (id: string) => void }) {
  return (
    <>
      {items.map((x, i) => (
        <Fragment key={x.id}>
          {i > 0 && "、"}
          <button type="button" className="ref-link" onClick={() => onOpen(x.id)}>{x.name}</button>
        </Fragment>
      ))}
    </>
  );
}
