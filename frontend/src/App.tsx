import { lazy, Suspense, useEffect } from "react";
import { startLive } from "./actions/live";
import { requestClose } from "./actions/window";
import { Rail } from "./features/shell/Rail";
import { MainView } from "./features/shell/MainView";
import { OverlayHost } from "./features/shell/OverlayHost";
import { TitleBar } from "./features/shell/TitleBar";
import { getService } from "./services";
import { store, useAppState } from "./store";

/** The scene navigator is a development/test aid: __SCENES__ is false in production builds, so it is compiled out. */
const SceneNavigator = __SCENES__ ? lazy(() => import("./dev/SceneNavigator")) : null;

/** Files dropped outside a drop zone must not make the window navigate to them. */
function usePreventFileNavigation() {
  useEffect(() => {
    const stop = (e: DragEvent) => e.preventDefault();
    document.addEventListener("dragover", stop);
    document.addEventListener("drop", stop);
    return () => {
      document.removeEventListener("dragover", stop);
      document.removeEventListener("drop", stop);
    };
  }, []);
}

/** Facts about the host and the user's choices that the stylesheet reads from the root element. */
function useDocumentAttributes() {
  const theme = useAppState((s) => s.theme);
  const redact = useAppState((s) => s.settings.redact);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.dataset.redact = redact || "black";
  }, [theme, redact]);
}

/** Window chrome. On a desktop host the system draws the window frame and the title bar is the draggable region;
 *  in a browser (dev/tests) a stand-in frame is drawn. */
export default function App() {
  const host = getService().host;
  const version = useAppState((s) => s.update.current);
  usePreventFileNavigation();
  useDocumentAttributes();
  useEffect(() => {
    const svc = getService();
    document.documentElement.dataset.host = svc.host;
    let alive = true;
    svc.appInfo().then((info) => {
      if (alive) store.update((d) => { d.update.current = info.version; });
    }).catch(() => {});
    const off = svc.onCloseRequested(requestClose);
    const offLive = startLive();
    return () => {
      alive = false;
      off();
      offLive();
    };
  }, []);

  return (
    <>
      <div className="win" id="win" data-host={host} data-version={version || undefined}>
        <TitleBar />
        <Rail />
        <MainView />
        <OverlayHost />
      </div>
      {SceneNavigator && (
        <Suspense fallback={null}>
          <SceneNavigator />
        </Suspense>
      )}
    </>
  );
}
