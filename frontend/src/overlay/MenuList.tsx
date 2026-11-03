import { Icon } from "../ui";
import { closePop, type MenuEntry, type MenuItem } from "./store";

/** The entries of a menu: headers, separators and choices (the current one is ticked). */
export function MenuList({ items, onPick }: { items: MenuEntry[]; onPick: (item: MenuItem) => void }) {
  return (
    <>
      {items.map((it, i) =>
        it === "-" ? (
          <div className="msep" key={i} />
        ) : "h" in it ? (
          <div className="mh" key={i}>
            {it.h}
          </div>
        ) : (
          <button
            key={i}
            type="button"
            className={it.d ? "mi d" : "mi"}
            role="menuitem"
            disabled={it.off}
            aria-disabled={it.off || undefined}
            onClick={() => {
              closePop();
              onPick(it);
            }}
          >
            {it.ic && <Icon name={it.ic} size={15} />}
            <span>{it.l}</span>
            {it.s && <small>{it.s}</small>}
            {it.on && (
              <span className="ck">
                <Icon name="check" size={14} />
              </span>
            )}
          </button>
        ),
      )}
    </>
  );
}
