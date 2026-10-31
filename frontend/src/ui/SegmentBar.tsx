export interface BarSegment {
  count: number;
  color: string;
  title?: string;
}

/** A horizontal bar divided into coloured shares of `total`. Empty segments are left out. */
export function SegmentBar({ segments, total, label }: { segments: BarSegment[]; total: number; label?: string }) {
  return (
    <div className="bar" aria-label={label}>
      {segments.map((s, i) => (s.count ? <i key={i} style={{ width: (s.count / total) * 100 + "%", background: s.color }} title={s.title ? s.title + " " + s.count : undefined} /> : null))}
    </div>
  );
}
