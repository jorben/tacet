import type { ModalSpec } from "../../overlay/store";
import { UpdateAppDialog, AboutLinkDialog, LicensesDialog, CleanWorkspaceDialog, CloseWindowDialog } from "./appDialogs";
import { BulkConfirmDialog, ExportReadyDialog, LiveBlockedDialog, LivePasswordDialog, PasswordDialog } from "./batchDialogs";
import { DeleteCategoryDialog, DetectionLossDialog, ImportConflictDialog, RuleEditorDialog, VersionsDialog } from "./configDialogs";
import { HistoryItemDialog, RecentTasksDialog, ResumeBatchDialog } from "./history";
import { PresetTestDialog } from "./presetTest";
import { InstallPluginDialog, UninstallPluginDialog } from "./pluginDialogs";
import { SlotConflictDialog, SwitchPresetDialog } from "./slots";

/** The dialog for a modal spec. Adding a dialog means adding a case here and a spec in `overlay/store.ts`. */
export function ModalContent({ spec }: { spec: ModalSpec }) {
  switch (spec.type) {
    case "recent": return <RecentTasksDialog />;
    case "historyItem": return <HistoryItemDialog entryId={spec.entryId} />;
    case "resumeBatch": return <ResumeBatchDialog entryId={spec.entryId} />;
    case "slotConflict": return <SlotConflictDialog go={spec.go} />;
    case "switchPreset": return <SwitchPresetDialog preset={spec.preset} go={spec.go} />;
    case "bulkConfirm": return <BulkConfirmDialog gid={spec.gid} k={spec.k} />;
    case "password": return <PasswordDialog gid={spec.gid} members={spec.members} />;
    case "exportReady": return <ExportReadyDialog />;
    case "livePassword": return <LivePasswordDialog targets={spec.targets} />;
    case "liveBlocked": return <LiveBlockedDialog targets={spec.targets} />;
    case "ruleEditor": return <RuleEditorDialog rulesetId={spec.rulesetId} index={spec.index} category={spec.category} initial={spec.draft} />;
    case "detectionLoss": return <DetectionLossDialog action={spec.action} lost={spec.lost} noRules={spec.noRules} go={spec.go} back={spec.back} />;
    case "deleteCategory": return <DeleteCategoryDialog id={spec.id} back={spec.back} />;
    case "versions": return <VersionsDialog kind={spec.kind} />;
    case "importConflict": return <ImportConflictDialog kind={spec.kind} name={spec.name} />;
    case "installPlugin": return <InstallPluginDialog />;
    case "uninstallPlugin": return <UninstallPluginDialog id={spec.id} />;
    case "updateApp": return <UpdateAppDialog />;
    case "aboutLink": return <AboutLinkDialog which={spec.which} />;
    case "licenses": return <LicensesDialog />;
    case "cleanWorkspace": return <CleanWorkspaceDialog />;
    case "closeWindow": return <CloseWindowDialog />;
    case "presetTest": return <PresetTestDialog />;
  }
}
