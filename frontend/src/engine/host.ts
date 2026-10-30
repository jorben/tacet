// Bridge from the UI engine to the host (Wails desktop or a plain browser). The engine never imports Wails.
import { getService } from '../services';
import { toast } from './layer';

export function hostMinimise() {
  void getService().minimiseWindow().catch(() => {});
}

/** In a browser there is no window to close; say so instead of silently doing nothing. */
export function hostQuit() {
  const s = getService();
  if (s.host === 'browser') {
    toast('浏览器预览：无法关闭窗口', undefined);
    return;
  }
  void s.quitApp().catch(() => {});
}
