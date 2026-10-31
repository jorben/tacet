import { cancelSingle } from "../../actions/single";
import type { Single } from "../../domain/types";
import { Center, FileIcon, IconButton, Steps } from "../../ui";

/** Reading and detecting the document: two steps with a stop button. */
export function ProcessingCard({ single }: { single: Single }) {
  return (
    <Center>
      <div className="card proc">
        <div className="proc-f">
          <FileIcon ext={single.ext} />
          <div style={{ minWidth: 0 }}>
            <div className="fn">{single.name}</div>
            <div className="fm">
              {single.size} · {single.pages.length} 页
            </div>
          </div>
          <IconButton icon="x" label="停止" tooltip="停止" onClick={cancelSingle} />
        </div>
        <div id="procSteps">
          <Steps labels={["读取", "检测"]} stage={single.stage} progress={single.sp} />
        </div>
      </div>
    </Center>
  );
}
