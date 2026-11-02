import { openSampleDrawer } from "../../actions/batch";
import { byCatalogOrder, categoryName } from "../../domain/categories";
import { fmt } from "../../domain/format";
import type { Batch, CategoryId } from "../../domain/types";
import { useAppState } from "../../store";
import { Button, List, Row, Swatch, categoryColor } from "../../ui";

const COUNT_STYLE = { minWidth: 60, textAlign: "right" } as const;

function SampleRowControls({ count, unit, onSample, hidden }: { count: number; unit: string; onSample?: () => void; hidden?: boolean }) {
  return (
    <>
      <span className="num" style={COUNT_STYLE}>
        {fmt(count)}
      </span>
      <span className="muted sm" style={{ width: 40 }}>
        {unit}
      </span>
      <Button variant="ghost" onClick={onSample} style={hidden ? { visibility: "hidden" } : undefined} tabIndex={hidden ? -1 : undefined} aria-hidden={hidden || undefined}>
        抽查
      </Button>
    </>
  );
}

/** What was handled without asking: counts per category, with a spot-check for each. */
export function AutoTab({ batch: B }: { batch: Batch }) {
  const detected = B.files.filter((f) => f.det && f.st !== "bad" && f.st !== "skipped");
  const catalog = useAppState((s) => s.categories);
  const total = (t: CategoryId) => detected.reduce((a, f) => a + (f.h[t] ?? 0), 0);
  // the categories this batch found, whatever they are, in catalog order
  const found = byCatalogOrder(catalog, detected.flatMap((f) => Object.keys(f.h).filter((t) => f.h[t])));
  const images = detected.reduce((a, f) => a + f.imgs, 0);
  return (
    <List>
      {found.map((t) => (
        <Row key={t} lead={<Swatch category={t} />} title={categoryName(catalog, t)} style={categoryColor(t)}>
          <SampleRowControls count={total(t)} unit="涂抹" onSample={() => openSampleDrawer(t)} />
        </Row>
      ))}
      <Row icon="image" iconSize={15} title="图像">
        <SampleRowControls count={images} unit="涂抹" onSample={() => openSampleDrawer("img")} />
      </Row>
      {B.widened ? (
        <Row icon="eye" iconSize={15} title="导出后残留" description="已自动扩大涂抹范围">
          <SampleRowControls count={B.widened} unit="处" hidden />
        </Row>
      ) : null}
    </List>
  );
}
