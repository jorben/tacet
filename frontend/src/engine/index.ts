// Entry of the ported prototype engine: installs global handlers and renders the first view.
import { render } from './shell';
import { installEvents } from './events';
import { S } from './state';
import { HOME_ICON } from './runtime';
import { getService } from '../services';
import { closeWin } from './actions-close';

let started = false;

export interface StartOptions {
  /** Development/test only: mounts the scene navigator and #scene= deep links. */
  scenes?: boolean;
}

export async function start(opt: StartOptions = {}) {
  if (started) return;
  started = true;
  installEvents();
  const icon = document.getElementById('appIcon') as HTMLLinkElement | null;
  if (icon) icon.href = HOME_ICON;
  const svc = getService();
  document.documentElement.dataset.host = svc.host;
  svc.appInfo().then((info) => {
    S.update.current = info.version;
    document.getElementById('win')!.dataset.version = info.version;
  }).catch(() => {});
  svc.onCloseRequested(closeWin);
  render();
  if (__SCENES__ && opt.scenes) {
    // Loaded on demand so the production bundle carries no scene fixtures.
    const [{ installProto }, { scene }] = await Promise.all([import('../dev/navigator'), import('../dev/scenes')]);
    installProto();
    const hs = new URLSearchParams(location.hash.slice(1)).get('scene');
    if (hs) scene(hs);
  }
}
