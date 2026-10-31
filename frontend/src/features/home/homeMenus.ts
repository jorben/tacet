import { setHomePreset } from "../../actions/home";
import { openSubpage } from "../../actions/navigation";
import { getState } from "../../store";
import { openMenu, type PopAnchor } from "../../overlay/store";

/** Choose the scheme new documents are processed with, or go to manage them. */
export function openHomePresetMenu(anchor: PopAnchor): void {
  const s = getState();
  openMenu(anchor, [
    ...s.presets.map((p) => ({ l: p.name, s: p.ver, k: p.name, on: s.homePreset === p.name })),
    "-",
    { l: "管理方案", ic: "sliders", k: "_m" },
  ], (it) => {
    if (it.k === "_m") openSubpage("presets", { presetTab: "scheme" });
    else setHomePreset(String(it.k));
  }, { alignRight: true });
}
