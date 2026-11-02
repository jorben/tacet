import { addManualRedaction, setHitState, setSameAll } from "../../actions/single";
import { categoryName, manualCategories } from "../../domain/categories";
import { locAt } from "../../domain/format";
import { sameValueHits, type ManualSelection } from "../../domain/single";
import { useAppState } from "../../store";
import { Segmented, Swatch } from "../../ui";

/** Details of one hit and the choice to keep or redact it (and its twins with the same value). */
export function HitPopover({ hitId }: { hitId: number }) {
  const single = useAppState((s) => s.single);
  const catalog = useAppState((s) => s.categories);
  const hit = single?.hits[hitId];
  if (!single || !hit) return null;
  const same = sameValueHits(single, hit);
  return (
    <div className="hp">
      <div className="hp-h">
        <Swatch category={hit.type} />
        {categoryName(catalog, hit.type)}
        <span className="muted" style={{ fontWeight: 400 }}>
          · {hit.src}
        </span>
      </div>
      <div className="hp-v">{hit.text}</div>
      <div className="muted sm" style={{ margin: "-6px 0 12px" }}>
        {locAt(single.ext, hit)}
      </div>
      <Segmented full value={hit.state} onChange={setHitState} options={[{ value: "keep", label: "保留" }, { value: "redact", label: "涂抹" }]} />
      {same.length > 1 && (
        <label className="hp-same">
          <input type="checkbox" id="sameAll" checked={single.sameAll} onChange={(e) => setSameAll(e.target.checked)} /> 同值 {same.length} 处一起
        </label>
      )}
    </div>
  );
}

export function KeyboardHelp() {
  const keys: [string, string][] = [
    ["J / ↓", "下一处"], ["K / ↑", "上一处"], ["X", "保留 / 涂抹"], ["E", "原文 / 效果"], ["C", "左右对比"], ["⌘ Z", "撤销"], ["⌘ ↩", "导出"],
  ];
  return (
    <div className="keys">
      {keys.map(([k, what]) => (
        <KeyRow key={k} keys={k} what={what} />
      ))}
    </div>
  );
}
function KeyRow({ keys, what }: { keys: string; what: string }) {
  return (
    <>
      <kbd>{keys}</kbd>
      <span>{what}</span>
    </>
  );
}

/** Offered after selecting text in the document: which kind of information it is. */
export function SelectionPicker({ selection }: { selection: ManualSelection }) {
  const { text } = selection;
  const choices = manualCategories(useAppState((s) => s.categories));
  return (
    <>
      <div className="mh">涂抹「{text.length > 14 ? text.slice(0, 14) + "…" : text}」</div>
      <div className="typegrid">
        {choices.map((c) => (
          <button key={c.id} type="button" onClick={() => addManualRedaction(c.id, selection)}>
            <Swatch category={c.id} />
            {c.name}
          </button>
        ))}
      </div>
    </>
  );
}
