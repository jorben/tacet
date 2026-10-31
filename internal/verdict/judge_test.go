package verdict

import "testing"

func good() Facts {
	return Facts{Phase: PhasePublished, Validated: true, InputVerified: true, PluginQualified: true, CategoriesQualified: true}
}

func TestPublishReadinessGate(t *testing.T) {
	mut := func(f func(*Facts)) Facts { x := good(); f(&x); return x }
	cases := []struct {
		name   string
		f      Facts
		want   Readiness
		reason Reason
	}{
		{"all conditions hold", good(), ReadyFull, ""},
		{"not validated", mut(func(f *Facts) { f.Validated = false }), NotReady, ReasonNotValidated},
		{"input digest unverified", mut(func(f *Facts) { f.InputVerified = false }), NotReady, ReasonInputUnverified},
		{"pending exception", mut(func(f *Facts) { f.PendingExceptions = 1 }), NotReady, ReasonPending},
		{"undecided gap", mut(func(f *Facts) { f.UnresolvedGaps = 2 }), NotReady, ReasonGaps},
		{"kept gap not eligible", mut(func(f *Facts) { f.RetainedGaps = 1 }), NotReady, ReasonPartialBlocked},
		{"kept gap eligible", mut(func(f *Facts) { f.RetainedGaps = 1; f.PartialEligible = true }), ReadyPartial, ""},
		{"pending beats kept gap", mut(func(f *Facts) { f.RetainedGaps = 1; f.PartialEligible = true; f.PendingExceptions = 1 }), NotReady, ReasonPending},
		{"undecided gap beats partial", mut(func(f *Facts) { f.RetainedGaps = 1; f.PartialEligible = true; f.UnresolvedGaps = 1 }), NotReady, ReasonGaps},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := PublishReadiness(c.f)
			if got != c.want || reason != c.reason {
				t.Fatalf("got %s/%s want %s/%s", got, reason, c.want, c.reason)
			}
		})
	}
}

func TestJudgeStatuses(t *testing.T) {
	mut := func(f func(*Facts)) Facts { x := good(); f(&x); return x }
	cases := []struct {
		name string
		f    Facts
		want Status
	}{
		{"published in full", good(), CompletedPerPolicy},
		{"published but plugin lacks grant", mut(func(f *Facts) { f.PluginQualified = false }), Delivered},
		{"published partial", mut(func(f *Facts) { f.RetainedGaps = 1; f.PartialEligible = true }), PartialResult},
		{"published but gate no longer holds is never complete", mut(func(f *Facts) { f.PendingExceptions = 1 }), Delivered},
		{"processing", Facts{Phase: PhaseProcessing}, Processing},
		{"publishing", Facts{Phase: PhasePublishing}, Processing},
		{"awaiting review", Facts{Phase: PhaseAwaitingReview}, AwaitingReview},
		{"ready without grant", mut(func(f *Facts) { f.Phase = PhaseReady }), AwaitingPublish},
		{"ready with grant", mut(func(f *Facts) { f.Phase = PhaseReady; f.PublishGranted = true }), Processing},
		{"ready but gate fails", mut(func(f *Facts) { f.Phase = PhaseReady; f.UnresolvedGaps = 1 }), Blocked},
		{"blocked", Facts{Phase: PhaseBlocked}, Blocked},
		{"failed", Facts{Phase: PhaseFailed}, ProcessingFailed},
		{"unprocessable", Facts{Phase: PhaseUnprocessable}, Unprocessable},
		{"skipped", Facts{Phase: PhaseSkipped}, Skipped},
		{"cancelled", Facts{Phase: PhaseCancelled}, Cancelled},
		{"unknown phase never reads as finished", Facts{Phase: "teapot", Validated: true, InputVerified: true}, Processing},
		{"zero value", Facts{}, Processing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Judge(c.f); got != c.want {
				t.Fatalf("got %s want %s", got, c.want)
			}
		})
	}
}

func TestUnattendedEligibility(t *testing.T) {
	if !UnattendedEligible(good()) {
		t.Fatal("clean policy-only file must qualify")
	}
	for name, f := range map[string]func(*Facts){
		"human decision":       func(f *Facts) { f.HumanDecided = true },
		"unqualified plugin":   func(f *Facts) { f.PluginQualified = false },
		"unqualified category": func(f *Facts) { f.CategoriesQualified = false },
		"partial result":       func(f *Facts) { f.RetainedGaps = 1; f.PartialEligible = true },
		"pending":              func(f *Facts) { f.PendingExceptions = 1 },
		"not validated":        func(f *Facts) { f.Validated = false },
	} {
		x := good()
		f(&x)
		if UnattendedEligible(x) {
			t.Fatalf("%s must not qualify", name)
		}
	}
}

func TestSummarizeBatchPhasesAndResultsPage(t *testing.T) {
	cases := []struct {
		name    string
		members []Status
		phase   BatchPhase
		results bool
		deliver int
	}{
		{"empty batch is never all delivered", nil, BatchEndedWithGaps, false, 0},
		{"all delivered", []Status{CompletedPerPolicy, Delivered}, BatchAllDelivered, true, 2},
		{"still running wins", []Status{Delivered, Processing, Blocked}, BatchRunning, false, 1},
		{"waiting for the user", []Status{Delivered, AwaitingReview}, BatchWaitingUser, false, 1},
		{"awaiting publish counts as waiting", []Status{Delivered, AwaitingPublish}, BatchWaitingUser, false, 1},
		{"failed member still offers reprocess", []Status{Delivered, ProcessingFailed}, BatchWaitingUser, false, 1},
		{"ended with partial and skipped", []Status{Delivered, PartialResult, Skipped, Unprocessable}, BatchEndedWithGaps, true, 1},
		{"nothing delivered but ended", []Status{Skipped, Unprocessable}, BatchEndedWithGaps, true, 0},
		{"stopped", []Status{Cancelled, Cancelled}, BatchStopped, false, 0},
		{"cancelled after some delivery is not stopped-clean", []Status{Delivered, Cancelled}, BatchEndedWithGaps, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Summarize(c.members)
			if s.Phase != c.phase || s.ShowResults != c.results || s.Delivered != c.deliver || s.Total != len(c.members) {
				t.Fatalf("got %+v", s)
			}
		})
	}
}

func TestExitCodeOnlyZeroWhenEverythingDelivered(t *testing.T) {
	if ExitCode([]Status{CompletedPerPolicy, Delivered}) != ExitAllPublished {
		t.Fatal("all delivered must be 0")
	}
	for _, bad := range []Status{PartialResult, Skipped, Cancelled, Blocked, ProcessingFailed, Unprocessable, AwaitingReview, AwaitingPublish, Processing, "unknown"} {
		if ExitCode([]Status{Delivered, bad}) != ExitIncomplete {
			t.Fatalf("%s must give 2", bad)
		}
	}
	if ExitCode(nil) != ExitIncomplete {
		t.Fatal("empty batch must not exit 0")
	}
}
