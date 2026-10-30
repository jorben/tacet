import { useEffect } from "react";
import { start } from "./engine";
import { getService } from "./services";

/** The scene navigator is a development/test aid: __SCENES__ is false in production builds, so it is compiled out. */
const SCENES = __SCENES__;

// Window chrome. On a desktop host the system draws the window frame and (on macOS) the traffic lights;
// the title bar below is the draggable region. In a browser (dev/tests) the engine draws a stand-in frame.
export default function App() {
  const host = getService().host;
  useEffect(() => {
    void start({ scenes: SCENES });
  }, []);

  return (
    <>
      <div className="win" id="win" data-host={host}>
        <header className="titlebar">
          {host === "browser" && (
            <div className="lights">
              <button data-a="closeWin" aria-label="关闭窗口"></button>
              <button aria-label="最小化" tabIndex={-1}></button>
              <button aria-label="缩放" tabIndex={-1}></button>
            </div>
          )}
          <span id="tbBack"></span>
          <div className="tb-title" id="tbTitle"></div>
          <div className="tb-right">
            <button className="btn btn-g model-shell" id="modelShell" data-a="modelManage" hidden></button>
            <span id="networkState" className="ib" data-tip="本机处理 · 未联网" data-tr="" tabIndex={0} aria-label="本机处理 · 未联网"></span>
          </div>
        </header>
        <nav className="rail" id="rail" aria-label="主导航"></nav>
        <main id="main"></main>
        <div id="layer"></div>
      </div>
      {SCENES && <div id="proto"></div>}
      <input type="file" id="fileIn" multiple hidden accept=".pdf,.docx,.txt,.md,.log,.csv,.jsonl,.json" />
    </>
  );
}
