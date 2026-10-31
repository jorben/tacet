import type { ReactNode } from "react";
import { Icon } from "./Icon";
import { tip } from "./tip";

export interface Stat {
  value: ReactNode;
  label: ReactNode;
  warn?: boolean;
}

/** Row of big numbers with captions. */
export function StatList({ stats }: { stats: Stat[] }) {
  return (
    <div className="stats">
      {stats.map((s, i) => (
        <div key={i}>
          <b className="num" style={s.warn ? { color: "var(--warn)" } : undefined}>
            {s.value}
          </b>
          <span>{s.label}</span>
        </div>
      ))}
    </div>
  );
}

interface ResultCardProps {
  /** Warning state (partial results) instead of success. */
  warn?: boolean;
  title: ReactNode;
  /** Line under the title, usually where the output went. */
  subtitle?: ReactNode;
  stats?: Stat[];
  /** Extra content between the stats and the actions. */
  note?: ReactNode;
  children?: ReactNode;
  /** Show the “按策略处理，未声明零遗漏” footnote. */
  disclaimer?: boolean;
}

/** The card that closes a task: icon, headline, numbers and next steps. */
export function ResultCard({ warn, title, subtitle, stats, note, children, disclaimer }: ResultCardProps) {
  return (
    <div className="card donecard">
      <div className={warn ? "done-ic warn" : "done-ic"}>
        <Icon name={warn ? "alert" : "check"} size={28} strokeWidth={2.2} />
      </div>
      <h2>{title}</h2>
      {subtitle}
      {stats && <StatList stats={stats} />}
      {note}
      {children}
      {disclaimer && (
        <div className="foot-note" {...tip("自动识别有局限，不代表文档已无隐私", { placement: "above" })}>
          <Icon name="info" size={12} />
          按策略处理，未声明零遗漏
        </div>
      )}
    </div>
  );
}
