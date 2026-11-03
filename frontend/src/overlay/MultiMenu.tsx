import { Icon } from "../ui";
import { closePop, toggleMultiItem, type MultiItem } from "./store";

/**
 * A menu where several choices are ticked. Ticking does not close it, and nothing is handed over while it is open:
 * closing it (Done, a click outside, Esc, another menu or dialog) hands over what is ticked, once.
 */
export function MultiMenu({ title, items, selected }: { title: string; items: MultiItem[]; selected: string[] }) {
  return (
    <>
      <div className="mh">{title}</div>
      {items.map((it) => {
        const on = selected.includes(it.k);
        return (
          <button key={it.k} type="button" className="mi" role="menuitemcheckbox" aria-checked={on} onClick={() => toggleMultiItem(it.k)}>
            <span className={it.k === it.l ? "mono" : undefined}>{it.l}</span>
            {it.s && <small>{it.s}</small>}
            <span className="ck" style={on ? undefined : { visibility: "hidden" }}>
              <Icon name="check" size={14} />
            </span>
          </button>
        );
      })}
      <div className="msep" />
      <button type="button" className="mi multi-done" onClick={closePop}>
        <span>完成</span>
      </button>
    </>
  );
}
