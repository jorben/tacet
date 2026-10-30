// Needs a build that includes the scene navigator: VITE_SCENES=1 npm run build && npx vite preview --port 4173
// Fidelity check: for every prototype scene, compare the visible text of docs/v1/tacet-prototype.html
// with the React app (same viewport, same scene). Prints a diff summary; exits non-zero on any difference.
import { launch } from "./walk-lib.mjs";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const BASE = process.env.BASE_URL || "http://127.0.0.1:4173/";
const PROTO = pathToFileURL(resolve(new URL("../../docs/v1/tacet-prototype.html", import.meta.url).pathname)).href;
const SCENES = ["home","fresh","model-download","model-paused","model-offline","model-disk","model-integrity","model-loading","model-load","model-wait","model-batch","model-plugins","s1","s2","s5","s4","s3","b1","b2","b5","b3","b6","b4","presets","plugins","settings"];

const browser = await launch();
async function grab(url, k) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 920 } });
  await page.goto(url + "#scene=" + k);
  await page.reload();
  await page.waitForTimeout(k === "s3" ? 2500 : 700);
  const out = await page.evaluate(() => ({
    // The version string is read from the host in the app (hard-coded 1.0.0 in the prototype): not a fidelity difference.
    text: document.querySelector("#win").innerText.replace(/\s+/g, " ").trim().replace(/(检查更新 )(1\.0\.0|0\.0\.0-dev)/, "$1<version>"),
    html: document.querySelector("#main").innerHTML.length,
  }));
  await page.close();
  return out;
}
let bad = 0;
for (const k of SCENES) {
  const a = await grab(PROTO, k);
  const b = await grab(BASE, k);
  const same = a.text === b.text;
  if (!same) bad++;
  console.log((same ? "SAME " : "DIFF ") + k + (same ? "" : "\n  proto: " + a.text.slice(0, 220) + "\n  react: " + b.text.slice(0, 220)));
}
await browser.close();
process.exit(bad ? 1 : 0);
