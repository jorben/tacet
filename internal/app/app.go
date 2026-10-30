package app

// Name is the product name.
const Name = "Tacet"

// Version is set at build time with
// -ldflags "-X github.com/jorben/tacet/internal/app.Version=...".
var Version = "0.0.0-dev"

// Error codes owned by the app layer. Other public codes are owned by their
// modules: workspace.CodeBusy, netgate.CodeActivityBusy and the version and
// scope conflicts of config and review (module design 4.1).
const (
	CodeQuickTaskActive = "QUICK_TASK_ACTIVE"
	CodeBatchActive     = "BATCH_ACTIVE"
)
