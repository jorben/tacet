package netgate

// CodeActivityBusy is returned when a lease of a conflicting kind is held.
const CodeActivityBusy = "ACTIVITY_BUSY"

// LeaseKind is the kind of activity holding a lease. A processing lease
// excludes network and maintenance leases (MD-19).
type LeaseKind string

const (
	Processing  LeaseKind = "processing"  // held by sched while document stages run
	Network     LeaseKind = "network"     // model, update and plugin downloads
	Maintenance LeaseKind = "maintenance" // plugin deployment, applying updates
)
