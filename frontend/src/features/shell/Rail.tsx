import { goTo } from "../../actions/navigation";
import type { View } from "../../domain/types";
import { useAppState } from "../../store";
import { pendingBatchDot } from "../../store/selectors";
import { Icon, tip } from "../../ui";

/** Which rail button is lit for each page. */
const SECTION: Record<View, View> = { home: "home", single: "home", batch: "home", history: "home", presets: "presets", plugins: "plugins", settings: "settings" };

function RailButton({ target, icon, label, current, children }: { target: View; icon: string; label: string; current: View; children?: React.ReactNode }) {
  return (
    <button type="button" onClick={() => goTo(target)} aria-label={label} aria-current={current === target ? "page" : undefined} {...tip(label)}>
      <Icon name={icon} size={19} />
      {children}
    </button>
  );
}

/** Left-hand navigation. The dot on “任务” says a batch is waiting for the user. */
export function Rail() {
  const view = useAppState((s) => s.view);
  const waiting = useAppState(pendingBatchDot);
  const cur = SECTION[view];
  return (
    <nav className="rail" id="rail" aria-label="主导航">
      <RailButton target="home" icon="inbox" label="任务" current={cur}>
        {waiting && <span className="dotb" />}
      </RailButton>
      <RailButton target="presets" icon="layers" label="方案" current={cur} />
      <RailButton target="plugins" icon="puzzle" label="插件" current={cur} />
      <span className="sp" />
      <RailButton target="settings" icon="gear" label="设置" current={cur} />
    </nav>
  );
}
