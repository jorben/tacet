//go:build darwin

package platform

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The sleeper is this test binary, not /bin/sleep: the kernel withholds the
// environment of SIP-protected system binaries from other processes, while a
// plugin worker (a managed interpreter) is an ordinary binary whose environment
// can be read. See m0-progress.md.
const helperEnv = "TACET_TEST_HELPER_SLEEP"

const escaper = `perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV' "$@" & echo $!; wait`

func TestHelperSleep(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("helper process only")
	}
	time.Sleep(300 * time.Second)
}

func startEscaper(t *testing.T, marker string) (*Tree, *exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", escaper, "sh", os.Args[0], "-test.run=^TestHelperSleep$")
	cmd.Env = []string{"PATH=/usr/bin:/bin", helperEnv + "=1"}
	if marker != "" {
		if err := Mark(cmd, marker); err != nil {
			t.Fatal(err)
		}
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let perl finish setsid + exec
	// the group kill cannot reach the escaped descendant, so kill it by pid
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL); tree.Kill(); cmd.Wait(); tree.Release() })
	return tree, cmd, pid
}

func TestSetsidDescendantEscapesTreeKillButNotMarkedReap(t *testing.T) {
	marker := "gen_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	tree, cmd, pid := startEscaper(t, marker)
	t.Cleanup(func() { ReapMarked(marker) })
	if !alive(pid) {
		t.Fatal("setup: escaped descendant not running")
	}
	if err := tree.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	time.Sleep(300 * time.Millisecond)
	if !alive(pid) {
		t.Skip("setsid descendant did not escape the group on this system; the limit is not reproduced")
	}
	// the documented limit is real: the group kill missed it. The marker finds it.
	n, err := ReapMarked(marker)
	if err != nil || n < 1 {
		t.Fatalf("reap: n=%d err=%v", n, err)
	}
	if !waitDead(pid, 3*time.Second) {
		t.Fatal("marked descendant survived ReapMarked")
	}
}

func TestReapMarkedOnlyTouchesTheGivenMarker(t *testing.T) {
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	_, _, mine := startEscaper(t, "gen_a_"+stamp)
	_, _, other := startEscaper(t, "gen_b_"+stamp)
	_, _, unmarked := startEscaper(t, "")
	n, err := ReapMarked("gen_a_" + stamp)
	if err != nil || n < 1 {
		t.Fatalf("reap: n=%d err=%v", n, err)
	}
	if !waitDead(mine, 3*time.Second) {
		t.Fatal("process with the marker survived")
	}
	if !alive(other) || !alive(unmarked) {
		t.Fatal("a process with another or no marker was killed")
	}
}

func TestReapMarkedRejectsInvalidMarkerAndFindsNothingForUnknown(t *testing.T) {
	for _, bad := range []string{"", "short", "has space in it", "a=b_______", strings.Repeat("x", 65)} {
		if _, err := ReapMarked(bad); err != ErrMarker {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if n, err := ReapMarked("gen_none_" + strconv.FormatInt(time.Now().UnixNano(), 36)); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestMarkReplacesAnExistingValueAndKeepsNilEnvInherited(t *testing.T) {
	cmd := exec.Command("/bin/true")
	cmd.Env = []string{"A=1", MarkerEnv + "=old_value_1", "B=2"}
	if err := Mark(cmd, "new_value_1"); err != nil {
		t.Fatal(err)
	}
	count, last := 0, ""
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, MarkerEnv+"=") {
			count++
			last = e
		}
	}
	if count != 1 || last != MarkerEnv+"=new_value_1" || len(cmd.Env) != 3 {
		t.Fatalf("env: %v", cmd.Env)
	}
	inherited := exec.Command("/bin/true")
	if err := Mark(inherited, "inherit_me_1"); err != nil || len(inherited.Env) < 2 {
		t.Fatalf("nil env must stay inherited: %v %v", err, len(inherited.Env))
	}
	if Mark(exec.Command("/bin/true"), "bad marker") != ErrMarker {
		t.Fatal("invalid marker accepted")
	}
}

func TestOnlyEnvironmentEntriesMatch(t *testing.T) {
	// layout: argc, path, padding, argv..., env...
	build := func(args, env []string) []byte {
		raw := []byte{byte(len(args)), 0, 0, 0}
		raw = append(raw, []byte("/bin/x\x00\x00\x00")...)
		for _, a := range args {
			raw = append(raw, []byte(a+"\x00")...)
		}
		for _, e := range env {
			raw = append(raw, []byte(e+"\x00")...)
		}
		return raw
	}
	want := []byte(MarkerEnv + "=gen_12345678")
	if !hasEnvEntry(build([]string{"x"}, []string{"A=1", MarkerEnv + "=gen_12345678"}), want, false) {
		t.Fatal("environment entry not found")
	}
	if hasEnvEntry(build([]string{MarkerEnv + "=gen_12345678"}, []string{"A=1"}), want, false) {
		t.Fatal("an argument must not match")
	}
	if hasEnvEntry(build(nil, []string{MarkerEnv + "=gen_123456789"}), want, false) {
		t.Fatal("a longer value must not match")
	}
	// a prefix matches a longer value, in the environment only
	if !hasEnvEntry(build(nil, []string{MarkerEnv + "=gen_123456789"}), want, true) {
		t.Fatal("a prefix must match a longer value")
	}
	if hasEnvEntry(build([]string{MarkerEnv + "=gen_123456789"}, []string{"A=1"}), want, true) {
		t.Fatal("an argument must not match, prefix or not")
	}
	if hasEnvEntry(build(nil, []string{MarkerEnv + "=gen_1234"}), want, true) {
		t.Fatal("a shorter value is not a match")
	}
	for _, short := range [][]byte{nil, {1, 0, 0}, {1, 0, 0, 0, 'x'}} {
		if hasEnvEntry(short, want, false) {
			t.Fatal("truncated buffer matched")
		}
	}
}

const reapSelfEnv = "TACET_TEST_HELPER_REAP_SELF"

func TestHelperReapSelf(t *testing.T) {
	marker := os.Getenv(reapSelfEnv)
	if marker == "" {
		t.Skip("helper process only")
	}
	// this process itself carries MarkerEnv=marker; it must not signal itself
	if os.Getenv(MarkerEnv) != marker {
		t.Fatal("setup: helper does not carry the marker")
	}
	if _, err := ReapMarked(marker); err != nil {
		t.Fatal(err)
	}
}

func TestReapMarkedNeverSignalsItsOwnProcess(t *testing.T) {
	marker := "gen_self_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperReapSelf$")
	cmd.Env = []string{"PATH=/usr/bin:/bin", reapSelfEnv + "=" + marker, MarkerEnv + "=" + marker}
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper was killed or failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("helper did not finish")
	}
}

func TestReapByPrefixKillsOnlyWorkersOfThatWorkspaceWhateverTheirMarker(t *testing.T) {
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	mine1, mine2, other := "tw_"+stamp+"_g1_1", "tw_"+stamp+"_g7_42", "tx_"+stamp+"_g1_1"
	_, cmd1, pid1 := startEscaper(t, mine1)
	_, cmd2, pid2 := startEscaper(t, mine2)
	_, cmd3, pid3 := startEscaper(t, other)
	t.Cleanup(func() { ReapMarked(mine1); ReapMarked(mine2); ReapMarked(other) })
	_ = cmd1
	_ = cmd2
	_ = cmd3
	for _, pid := range []int{pid1, pid2, pid3} {
		if !alive(pid) {
			t.Skip("setup: a helper is not running")
		}
	}
	n, err := ReapMarkedPrefix("tw_" + stamp + "_")
	if err != nil || n < 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	time.Sleep(300 * time.Millisecond)
	if alive(pid1) || alive(pid2) {
		t.Fatal("workers of the workspace survived")
	}
	if !alive(pid3) {
		t.Fatal("a worker of another workspace was killed")
	}
	if _, err = ReapMarkedPrefix("bad prefix"); err != ErrMarker {
		t.Fatalf("%v", err)
	}
	// an exact marker still means exactly that marker, not everything that starts with it
	if n, _ := ReapMarked("tx_" + stamp); n != 0 || !alive(pid3) {
		t.Fatalf("exact match must not act as a prefix (n=%d)", n)
	}
}
