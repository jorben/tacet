/** A row of progress segments with labels: finished steps are full, the current one shows `progress` (0–100). */
export function Steps({ labels, stage, progress }: { labels: string[]; stage: number; progress: number }) {
  return (
    <>
      <div className="steps">
        {labels.map((l, i) => (
          <div className="step" key={l}>
            <i style={{ width: (i < stage ? 100 : i === stage ? progress : 0) + "%" }} />
          </div>
        ))}
      </div>
      <div className="steps-l">
        {labels.map((l, i) => (
          <span key={l} className={i === stage ? "on" : i < stage ? "done" : ""}>
            {l}
          </span>
        ))}
      </div>
    </>
  );
}
