// Entry of the ported prototype engine: installs global handlers and renders the first view.
import { render } from './shell';
import { installEvents } from './events';
import { installProto } from './protoBoot';
import { scene } from './proto';
import { HOME_ICON } from './runtime';

let started = false;
export function start() {
  if (started) return;
  started = true;
  installEvents();
  installProto();
  const icon = document.getElementById('appIcon') as HTMLLinkElement | null;
  if (icon) icon.href = HOME_ICON;
  render();
  const hs = new URLSearchParams(location.hash.slice(1)).get('scene');
  if (hs) scene(hs);
}
