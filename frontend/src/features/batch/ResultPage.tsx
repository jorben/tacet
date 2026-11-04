import { goTo } from "../../actions/navigation";
import { startReview } from "../../actions/review";
import { revealOutput } from "../../actions/single";
import { countFiles, exportedCount, openFileCount } from "../../domain/batch";
import { fmt } from "../../domain/format";
import type { BatchSummary } from "../../domain/types";
import { Button, Center, Icon, LinkButton, ResultCard } from "../../ui";

/** How a batch ended: how many files were exported and why the rest were not. */
export function ResultPage({ batch: B, onManifest }: { batch: BatchSummary; onManifest: () => void }) {
  const c = countFiles(B.files);
  const out = exportedCount(c);
  const missing = B.n - out;
  const open = openFileCount(B.files);
  const all = !missing && !c.partial;
  const stats = [
    { value: fmt(c.done), label: "已导出" }, { value: fmt(c.partial), label: "部分结果" }, { value: fmt(open), label: "待你处理" },
    { value: fmt(c.skip), label: "已跳过" }, { value: fmt(c.bad + c.held - open), label: "无法处理" },
  ].filter((s, i) => s.value !== "0" || !i);
  return (
    <Center>
      <ResultCard
        warn={!all}
        title={all ? "已全部导出" : "已导出 " + fmt(out) + " / " + fmt(B.n)}
        subtitle={
          <div className="fn">
            <Icon name="folder" size={13} />
            {B.out}
          </div>
        }
        stats={stats}
        note={
          missing > 0 && (
            <div className="muted sm" style={{ margin: "-10px 0 22px" }}>
              未导出 {fmt(missing)} 份
              {open ? (
                <>
                  ，其中 {fmt(open)} 份待你处理 <LinkButton onClick={() => startReview(null)}>去处理</LinkButton>
                </>
              ) : (
                "，原因见清单"
              )}
            </div>
          )
        }
        disclaimer
      >
        <div className="acts">
          <Button variant="primary" icon="folder" onClick={revealOutput}>
            打开导出文件夹
          </Button>
          <Button icon="list" onClick={onManifest}>
            查看清单
          </Button>
        </div>
        <div className="acts" style={{ marginTop: 8 }}>
          <LinkButton onClick={() => goTo("home")}>再处理一份</LinkButton>
        </div>
      </ResultCard>
    </Center>
  );
}
