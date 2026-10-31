import { useEffect, useRef } from "react";
import { decideItem, setReviewMode, stepReview, undoReview } from "../../actions/review";
import { currentItem } from "../../domain/review";
import { NO_DOCUMENT_GROUPS } from "../../domain/catalog";
import type { Batch } from "../../domain/types";
import { hasDialog } from "../../overlay/store";
import { useAppState } from "../../store";
import { ReviewDocument } from "./ReviewDocument";
import { ReviewDone } from "./ReviewDone";
import { ReviewPanel } from "./ReviewPanel";
import { ReviewQueue } from "./ReviewQueue";

/** Review of the batch's exceptions: queue on the left, the document in the middle, the decision on the right. */
export function ReviewPage({ batch: B }: { batch: Batch }) {
  const mode = useAppState((s) => s.docMode);
  const it = currentItem(B);
  const pwdRef = useRef<HTMLInputElement>(null);

  /** Deciding a locked file needs its password first. */
  const decide = (k: string) => {
    if (k === "pwd" && pwdRef.current && !pwdRef.current.value) {
      pwdRef.current.focus();
      return;
    }
    decideItem(k);
  };

  useEffect(() => {
    pwdRef.current?.focus({ preventScroll: true });
  }, [it?.key]);

  // Keyboard: J/K to move, A/B (or 1/2) to decide, E/C for the view, ⌘Z to undo.
  const latest = useRef({ B, decide, mode });
  latest.current = { B, decide, mode };
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.defaultPrevented || hasDialog()) return;
      const { B: batch, decide: decideNow, mode: viewMode } = latest.current;
      const target = ev.target as HTMLInputElement | null;
      if (target?.id === "rvPwd" && ev.key === "Enter") { ev.preventDefault(); decideNow("pwd"); return; }
      if (target?.tagName === "INPUT" && target.type !== "checkbox") return;
      const k = ev.key.toLowerCase();
      if ((ev.metaKey || ev.ctrlKey) && k === "z") { ev.preventDefault(); undoReview(); return; }
      if (ev.metaKey || ev.ctrlKey) return;
      if (k === "j" || ev.key === "ArrowDown") { ev.preventDefault(); stepReview(1); }
      else if (k === "k" || ev.key === "ArrowUp") { ev.preventDefault(); stepReview(-1); }
      else if (["1", "2", "a", "b"].includes(k)) {
        const cur = currentItem(batch);
        const act = cur && batch.groups[cur.gi]!.acts["1a".includes(k) ? 0 : 1];
        if (act) decideNow(act.k);
      } else if (k === "e") setReviewMode(viewMode === "orig" ? "fx" : "orig");
      else if (k === "c") setReviewMode(viewMode === "cmp" ? "orig" : "cmp");
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const cmp = !!it && mode === "cmp" && !NO_DOCUMENT_GROUPS.includes(it.gid);
  return (
    <div className={["rvw", !it && "fin", cmp && "cmp"].filter(Boolean).join(" ")}>
      <ReviewQueue batch={B} />
      {it ? (
        <>
          <ReviewDocument batch={B} item={it} mode={mode} onMode={setReviewMode} />
          <ReviewPanel batch={B} item={it} onDecide={decide} pwdRef={pwdRef} />
        </>
      ) : (
        <ReviewDone batch={B} />
      )}
    </div>
  );
}
