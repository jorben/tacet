//go:build darwin || windows

package platform

import "testing"

func TestActivityIsDeclaredAndRevokedOnlyWhenItsStateFlips(t *testing.T) {
	defer DeclareActivity(false)
	for _, step := range []struct{ on, want bool }{{true, true}, {true, true}, {false, false}, {false, false}, {true, true}, {false, false}} {
		if err := DeclareActivity(step.on); err != nil {
			t.Fatal(err)
		}
		if activityDeclared() != step.want {
			t.Fatalf("after DeclareActivity(%v): declared=%v", step.on, activityDeclared())
		}
	}
}
