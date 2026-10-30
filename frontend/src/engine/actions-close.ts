// @ts-nocheck
import { A } from './actions';

/** Entry for a user-initiated window close coming from the host (title-bar button, Cmd+W). */
export function closeWin() {
  A.closeWin();
}
