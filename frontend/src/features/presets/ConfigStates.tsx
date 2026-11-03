import type { ReactNode } from "react";
import { loadCategories } from "../../actions/liveConfig";
import { useAppState } from "../../store";
import { Icon, LinkButton } from "../../ui";

/**
 * Said on every tab when the catalog could not be read: the names shown are the ones read before (or the built-in
 * ones), so a category added elsewhere may be missing. “重试” reads it again.
 */
export function CatalogNotice() {
  const failed = useAppState((s) => s.categoryStatus === "failed");
  if (!failed) return null;
  return (
    <div className="config-note warn" role="alert">
      <Icon name="alert" size={14} />
      <span>类别目录读取失败，显示的是之前读到的类别，可能不是最新的。</span>
      <LinkButton onClick={() => void loadCategories()}>重试</LinkButton>
    </div>
  );
}

/** What the right side shows when there is nothing to edit yet, with the ways to get something. */
export function GuideCard({ icon, title, children, actions }: { icon: string; title: string; children: ReactNode; actions: ReactNode }) {
  return (
    <div className="guide-card" role="region" aria-label={title}>
      <span className="guide-icon"><Icon name={icon} size={20} /></span>
      <div className="guide-title">{title}</div>
      <p className="muted sm">{children}</p>
      <div className="guide-actions">{actions}</div>
    </div>
  );
}
