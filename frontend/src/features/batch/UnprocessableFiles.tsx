import { useAppState } from "../../store";

/** The files a precheck rejected, with the reason for each. */
export function UnprocessableFiles() {
  const files = useAppState((s) => s.batch?.files);
  const bad = (files || []).filter((f) => f.st === "bad");
  return (
    <>
      <div className="mh">无法处理 · 不会导出</div>
      {bad.map((f) => (
        <div className="mi ro" key={f.i}>
          <span>{f.name}</span>
          <small>{f.reason}</small>
        </div>
      ))}
    </>
  );
}
