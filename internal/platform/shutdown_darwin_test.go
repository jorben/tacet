//go:build darwin

package platform

import (
	"testing"
	"time"
)

func TestTheSystemEndingNotificationReachesTheHandler(t *testing.T) {
	got := make(chan struct{}, 4)
	OnSystemEnding(func() { got <- struct{}{} })
	postSystemEndingForTest()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the power-off notification was not delivered")
	}
}
