//go:build darwin

package platform

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitDead(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !alive(pid)
}

func TestKillReclaimsGrandchildrenAndIsIdempotent(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 300 & echo $!; sleep 300 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Release()
	reader := bufio.NewReader(out)
	var grandchildren []int
	for i := 0; i < 2; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatal(err)
		}
		grandchildren = append(grandchildren, pid)
	}
	for _, pid := range grandchildren {
		if !alive(pid) {
			t.Fatal("setup: grandchild not running")
		}
	}
	if err = tree.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = tree.Kill(); err != nil {
		t.Fatal("second kill must be a no-op")
	}
	cmd.Wait()
	for _, pid := range grandchildren {
		if !waitDead(pid, 3*time.Second) {
			t.Fatalf("grandchild %d survived tree kill", pid)
		}
	}
}

func TestKillAfterNormalExitDoesNotFail(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	if err = tree.Kill(); err != nil {
		t.Fatalf("kill of finished tree: %v", err)
	}
}

func TestStartTreeRejectsStartedOrMissingCommand(t *testing.T) {
	if _, err := StartTree(nil); err == nil {
		t.Fatal("nil command accepted")
	}
	if _, err := StartTree(exec.Command("/nonexistent/binary")); err != ErrProcessStart {
		t.Fatalf("start failure: %v", err)
	}
}
