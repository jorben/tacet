import { dismissToast, type ToastSpec } from "./store";

/** A short message at the bottom of the window, optionally with an undo. */
export function Toast({ toast }: { toast: ToastSpec }) {
  return (
    <div className="toast" role="status">
      {toast.msg}
      {toast.undo && (
        <button
          type="button"
          onClick={() => {
            const undo = toast.undo!;
            dismissToast();
            undo();
          }}
        >
          撤销
        </button>
      )}
    </div>
  );
}
