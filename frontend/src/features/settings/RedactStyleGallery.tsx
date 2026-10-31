import { Fragment } from "react";
import { setRedactStyle } from "../../actions/settings";
import type { RedactStyle } from "../../domain/types";
import { Icon, List } from "../../ui";

const GROUPS: [string, [RedactStyle, string][]][] = [
  ["经典", [["black", "纯黑"], ["gray", "深灰"], ["mosaic", "马赛克"], ["stripe", "斜纹"]]],
  ["趣味", [["heart", "粉色爱心"], ["paw", "小猫脚印"], ["snow", "雪花"], ["seal", "奉旨回避"], ["star", "星空"], ["dot", "柠檬波点"], ["rainbow", "彩虹"], ["plaid", "薄荷格纹"]]],
];

/** Pick how redacted text looks. Each tile previews the pattern on a line of text. */
export function RedactStyleGallery({ value }: { value: RedactStyle }) {
  return (
    <List className="rgal">
      {GROUPS.map(([heading, styles]) => (
        <Fragment key={heading}>
          <div className="rg-h">{heading}</div>
          <div className="rgrid" role="radiogroup" aria-label={"涂抹样式 · " + heading}>
            {styles.map(([v, label]) => {
              const on = value === v;
              return (
                <button key={v} type="button" className="rtile" role="radio" aria-checked={on} onClick={() => setRedactStyle(v)}>
                  <span className="pp">
                    姓名：<span className="rsw" data-redact={v}></span>
                  </span>
                  <span className="lb">
                    {on && <Icon name="check" size={12} strokeWidth={2.6} />}
                    {label}
                  </span>
                </button>
              );
            })}
          </div>
        </Fragment>
      ))}
    </List>
  );
}
