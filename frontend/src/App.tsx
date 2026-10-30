import { useEffect } from "react";
import { start } from "./engine";
import { getService } from "./services";

// The window chrome mirrors docs/v1/tacet-prototype.html; the engine fills #rail, #main, #layer and #proto.
export default function App() {
  useEffect(() => {
    start();
    getService()
      .appInfo()
      .then((info) => {
        const win = document.getElementById("win");
        if (win) win.dataset.version = info.version;
      })
      .catch(() => {});
  }, []);

  return (
    <>
      <div className="win" id="win">
        <header className="titlebar">
          <div className="lights">
            <button data-a="closeWin" aria-label="关闭窗口"></button>
            <button aria-label="最小化" tabIndex={-1}></button>
            <button aria-label="缩放" tabIndex={-1}></button>
          </div>
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
      <div id="proto"></div>
      <input type="file" id="fileIn" multiple hidden accept=".pdf,.docx,.txt,.md,.log,.csv,.jsonl,.json" />
    </>
  );
}
