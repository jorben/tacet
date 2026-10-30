// Runtime helpers shared by the engine: DOM shortcuts, formatting, a deterministic RNG and icon/asset helpers.
// Ported from the prototype (docs/v1/tacet-prototype.html, "utilities").
import { ICONS } from '../domain/icons';
import logoUrl from '../assets/logo.webp';
import flowUrl from '../assets/flow.webp';

export const HOME_ICON: string = logoUrl;
export const HOME_FLOW: string = flowUrl;

export const $ = (s: string, el: ParentNode = document): any => el.querySelector(s);
export const $$ = (s: string, el: ParentNode = document): any[] => [...el.querySelectorAll(s)];
export const esc = (s: unknown) =>
  String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c as '&']!);
export const fmt = (n: unknown) => Number(n).toLocaleString('zh-CN');

export function rng(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Mutable module-level state of the prototype (timers, the active RNG, open popovers). */
export const rt: any = {
  R: rng(417),
  popClose: null,
  menuCb: null,
  historyTrigger: null,
  toastT: null,
  toastUndo: null,
  procT: null,
  RFX: null,
  RNOID: false,
  bT: null,
};
export const ri = (a: number, b: number) => a + Math.floor(rt.R() * (b - a + 1));
export const pick = (a: any[]) => a[Math.floor(rt.R() * a.length)];

export const I = (n: string, s = 16, w = 1.75) =>
  '<svg class="i" width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="' + w +
  '" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + (ICONS[n] || '') + '</svg>';
export const LOGO = (s = 18) =>
  '<img src="' + HOME_ICON + '" width="' + s + '" height="' + s +
  '" alt="" aria-hidden="true" style="display:block;flex:none;object-fit:contain;border-radius:22%">';
export const fileIcon = (ext: string) =>
  '<span class="ficon file">' + I(ext === 'zip' ? 'folder' : 'file', 17) + '<b>' + esc((ext || '').toUpperCase().slice(0, 4)) + '</b></span>';
