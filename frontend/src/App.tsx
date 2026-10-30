import { useEffect, useState } from "react";
import { Service } from "../bindings/github.com/jorben/tacet/internal/gui";
import type { Info } from "../bindings/github.com/jorben/tacet/internal/gui/models";

export default function App() {
  const [info, setInfo] = useState<Info | null>(null);

  useEffect(() => {
    Service.AppInfo().then(setInfo).catch(() => setInfo(null));
  }, []);

  return (
    <main className="shell">
      <h1>Tacet</h1>
      <p className="muted">{info ? info.version : "…"}</p>
    </main>
  );
}
