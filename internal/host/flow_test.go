package host

import (
	"errors"
	"testing"
)

func TestFlowSeparatesAdmissionCommitAndCapacityRelease(t *testing.T) {
	flow, err := NewFlow(100, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = flow.Accept(1, 60); err != nil {
		t.Fatal(err)
	}
	if err = flow.Credit(60, 1); !errors.Is(err, ErrProtocolCredit) {
		t.Fatal("uncommitted item credited")
	}
	if err = flow.Commit(1); err != nil {
		t.Fatal(err)
	}
	if err = flow.Accept(2, 50); !errors.Is(err, ErrProtocolCredit) {
		t.Fatal("commit implicitly released memory")
	}
	if err = flow.Credit(60, 1); err != nil {
		t.Fatal(err)
	}
	if err = flow.Accept(2, 50); err != nil {
		t.Fatal(err)
	}
	if err = flow.End(2); !errors.Is(err, ErrProtocolSequence) {
		t.Fatal("uncommitted stream completed")
	}
	if err = flow.Commit(2); err != nil {
		t.Fatal(err)
	}
	if err = flow.End(2); err != nil {
		t.Fatal(err)
	}
	if err = flow.Accept(3, 1); !errors.Is(err, ErrProtocolSequence) {
		t.Fatal("late item accepted")
	}
}

func TestFlowRejectsSequenceJumpsAndBudgetInflation(t *testing.T) {
	flow, _ := NewFlow(10, 1)
	if err := flow.Accept(2, 1); !errors.Is(err, ErrProtocolSequence) {
		t.Fatal("sequence jump accepted")
	}
	if err := flow.Commit(1); !errors.Is(err, ErrProtocolCommit) {
		t.Fatal("future ack accepted")
	}
	if err := flow.Credit(1, 1); !errors.Is(err, ErrProtocolCredit) {
		t.Fatal("credit inflated")
	}
	if err := flow.Accept(1, 10); err != nil {
		t.Fatal(err)
	}
	if err := flow.Accept(2, 1); !errors.Is(err, ErrProtocolCredit) {
		t.Fatal("item limit ignored")
	}
	if err := flow.Commit(1); err != nil {
		t.Fatal(err)
	}
	if err := flow.Credit(11, 1); !errors.Is(err, ErrProtocolCredit) {
		t.Fatal("bytes inflated")
	}
	if err := flow.Credit(10, 2); !errors.Is(err, ErrProtocolCredit) {
		t.Fatal("items inflated")
	}
	if err := flow.Credit(10, 1); err != nil {
		t.Fatal(err)
	}
	if err := flow.Credit(1, 1); !errors.Is(err, ErrProtocolCredit) {
		t.Fatal("double credit")
	}
}

func TestFlowEmptyStreamAndInvalidLimits(t *testing.T) {
	for _, pair := range [][2]uint64{{0, 1}, {1, 0}, {MaxMessageBytes + 1, 1}, {1, 65537}} {
		if _, err := NewFlow(pair[0], pair[1]); err == nil {
			t.Fatal("invalid credit caps accepted")
		}
	}
	flow, _ := NewFlow(1, 1)
	if err := flow.End(0); err != nil {
		t.Fatal(err)
	}
	if err := flow.End(0); err == nil {
		t.Fatal("duplicate end")
	}
}
