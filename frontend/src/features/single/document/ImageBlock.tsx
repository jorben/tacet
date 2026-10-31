import { openImageMenu } from "../../../actions/single";
import { outputKind } from "../../../domain/format";
import type { DocImage } from "../../../domain/types";
import { tip } from "../../../ui";
import { PhotoArt, SealArt } from "./artwork";

interface ImageBlockProps {
  image: DocImage;
  index: number;
  ext: string;
  revealed: boolean;
  /** Opened from a batch: the image setting follows the batch and cannot be changed here. */
  readOnly: boolean;
}

/** An image of the document: shown, marked for removal, or (in the effect view) already blocked out. */
export function ImageBlock({ image, index, ext, revealed, readOnly }: ImageBlockProps) {
  const removed = image.act !== "keep";
  const gone = removed && revealed;
  const cls = (image.kind === "photo" ? "d-photo" : "d-seal") + (gone ? " gone" : removed ? " imgm" : "");
  const label = image.label + " · " + (removed ? outputKind(ext).verb : "保留") + (readOnly ? " · 按批次设置" : "");
  return (
    <div
      className={cls}
      style={readOnly ? { cursor: "default" } : undefined}
      data-v={index}
      {...tip(label)}
      onClick={readOnly ? undefined : (e) => { e.stopPropagation(); openImageMenu(e.currentTarget, index); }}
    >
      {gone ? "图像已涂抹" : image.kind === "photo" ? <PhotoArt /> : <SealArt />}
    </div>
  );
}
