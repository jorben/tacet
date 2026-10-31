import { useRef, useState, type DragEvent } from "react";
import { type IncomingFile, takeFiles } from "../../actions/home";
import { Icon } from "../../ui";
import { openHomePresetMenu } from "./homeMenus";

const FORMATS = ["DOCX", "PDF", "TXT", "CSV", "JSONL"];

export const toIncoming = (files: FileList | File[]): IncomingFile[] => [...files].map((f) => ({ name: f.name, size: f.size }));

/** Where documents are dropped or picked, with the scheme they will be processed with. */
export function DropZone({ compact, preset }: { compact: boolean; preset: string }) {
  const [over, setOver] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    setOver(false);
    takeFiles(toIncoming(e.dataTransfer.files));
  };
  return (
    <div
      className="drop-wrap"
      id="drop"
      onDragEnter={(e) => { e.preventDefault(); setOver(true); }}
      onDragOver={(e) => { e.preventDefault(); setOver(true); }}
      onDragLeave={(e) => { e.preventDefault(); if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false); }}
      onDrop={onDrop}
    >
      <button type="button" className={["drop", compact && "sm", over && "over"].filter(Boolean).join(" ")} aria-label="选择待脱敏文档，或将文档拖入此处" onClick={() => input.current?.click()}>
        <span className="drop-g" aria-hidden="true">
          <Icon name="file" size={56} strokeWidth={1.5} />
          <span className="drop-plus">
            <Icon name="plus" size={23} />
          </span>
        </span>
        <span className="drop-t">拖入文档</span>
        <span className="drop-s">
          <span>或点击选择 ·</span>
          <span className="drop-formats">
            {FORMATS.map((f) => (
              <span key={f}>{f}</span>
            ))}
          </span>
        </span>
      </button>
      <button type="button" className="sel drop-preset" aria-label={"脱敏方案：" + preset} onClick={(e) => openHomePresetMenu(e.currentTarget)}>
        <Icon name="layers" size={18} />
        <span>{preset}</span>
        <Icon name="chev-d" size={15} />
      </button>
      <input
        ref={input}
        type="file"
        id="fileIn"
        multiple
        hidden
        accept=".pdf,.docx,.txt,.md,.log,.csv,.jsonl,.json"
        onChange={(e) => {
          takeFiles(toIncoming(e.target.files || []));
          e.target.value = "";
        }}
      />
    </div>
  );
}
