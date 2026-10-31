import { useEffect } from "react";
import { takeFiles } from "../../actions/home";
import flowUrl from "../../assets/flow.webp";
import { useAppState } from "../../store";
import { Icon, LOGO_URL, Page } from "../../ui";
import { HomeModelGuide } from "../models/HomeModelGuide";
import { openModal } from "../../overlay/store";
import { DropZone, toIncoming } from "./DropZone";
import { HistoryRows } from "./HistoryRows";
import { useLiveHistory } from "./useLiveHistory";

/** Documents dropped anywhere outside the drop zone count too. */
function useWindowDrop() {
  useEffect(() => {
    const onDrop = (e: DragEvent) => {
      if (!(e.target as Element | null)?.closest?.("#drop") && e.dataTransfer) takeFiles(toIncoming(e.dataTransfer.files));
    };
    document.addEventListener("drop", onDrop);
    return () => document.removeEventListener("drop", onDrop);
  }, []);
}

export function HomePage() {
  const presets = useAppState((s) => s.presets);
  const homePreset = useAppState((s) => s.homePreset);
  const history = useLiveHistory();
  useWindowDrop();
  const preset = presets.some((p) => p.name === homePreset) ? homePreset : presets[0]!.name;
  return (
    <>
      <div className="home-flow" aria-hidden="true">
        <img src={flowUrl} width={2070} height={365} alt="" draggable={false} />
      </div>
      <Page
        className="home-page"
        innerClassName="home-inner"
        aside={
          <aside className="home-note" aria-label="敏感信息，止于此处。本地处理，守护更安心。">
            <strong className="home-note-title">
              敏感信息，
              <br />
              止于此处。
            </strong>
            <span className="home-note-caption">本地处理，守护更安心。</span>
          </aside>
        }
      >
        <header className="home-brand">
          <img className="home-brand-icon" src={LOGO_URL} width={80} height={80} alt="Tacet 幼苗角色 Logo" />
          <h1>Tacet</h1>
          <p>该静默处，自有分寸</p>
        </header>
        <HomeModelGuide />
        <DropZone compact={history.length > 0} preset={preset} />
        {history.length > 0 && (
          <section aria-labelledby="recentTitle">
            <div className="recent-head">
              <h2 id="recentTitle">最近</h2>
              <button type="button" className="history-all" onClick={() => openModal({ type: "recent" })}>
                查看全部 <Icon name="chev-r" size={16} />
              </button>
            </div>
            <div className="list home-history">
              <HistoryRows entries={history.slice(0, 3)} />
            </div>
          </section>
        )}
      </Page>
    </>
  );
}
