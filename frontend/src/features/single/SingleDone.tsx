import { goTo } from "../../actions/navigation";
import { revealOutput } from "../../actions/single";
import { outputName } from "../../domain/format";
import { keptImageCount, redactedCount } from "../../domain/single";
import type { Single } from "../../domain/types";
import { Button, Center, Icon, LinkButton, ResultCard } from "../../ui";

/** A single file has been exported. Also shows archived results from “最近”. */
export function SingleDone({ single, onManifest }: { single: Single; onManifest: () => void }) {
  const red = redactedCount(single);
  const kept = single.hits.length - red;
  const keptImages = keptImageCount(single);
  const partial = keptImages > 0;
  return (
    <Center>
      <ResultCard
        warn={partial}
        title={partial ? "已导出部分结果" : "已导出"}
        subtitle={
          <div className="fn">
            <Icon name="folder" size={13} />
            {single.out} / {outputName(single.name, single.ext)}
          </div>
        }
        stats={[
          { value: red, label: "涂抹" },
          { value: kept, label: "保留" },
          { value: single.imgs.length - keptImages, label: "涂抹图像" },
          ...(partial ? [{ value: keptImages, label: "未检查图像", warn: true }] : []),
        ]}
        disclaimer
      >
        <div className="acts">
          <Button variant="primary" icon="folder" onClick={revealOutput}>
            打开所在位置
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
