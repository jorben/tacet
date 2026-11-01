//go:build darwin

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const ptyEnv = "TACET_PLATFORM_TEST_PTY"

// TestPasswordHelper runs on the terminal the parent test opened: it reads a password and reports only its
// length and whether echo is back on afterwards.
func TestPasswordHelper(t *testing.T) {
	if os.Getenv(ptyEnv) == "" {
		t.Skip("helper process only")
	}
	if !IsTerminal(os.Stdin) {
		fmt.Println("NOT_A_TERMINAL")
		return
	}
	// the driver types as soon as the prompt shows; the pause holds the moment open, so a prompt shown before
	// echo is off would let the typing be echoed every time, not only under load
	pw, err := ReadPassword(context.Background(), os.Stdin, func() { fmt.Print("PROMPT>"); time.Sleep(300 * time.Millisecond) })
	st, _ := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
	fmt.Printf("\nGOT:%d ERR:%v ECHO:%v DONE\n", len(pw), err, st != nil && st.Lflag&unix.ECHO != 0)
	clear(pw)
}

// Through a real pseudo-terminal: what is typed is not echoed back, the whole line is returned, and echo
// is on again afterwards.
const ptyDriver = `
import os, pty, sys, time
pid, fd = pty.fork()
if pid == 0:
    os.execve(sys.argv[1], [sys.argv[1], '-test.run=^TestPasswordHelper$', '-test.v'], dict(os.environ, ` + ptyEnv + `='1'))
out = b''
def until(token):
    global out
    deadline = time.time() + 20
    while token not in out and time.time() < deadline:
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        out += chunk
until(b'PROMPT>')
os.write(fd, b'hunter2-secret\n')
until(b'DONE')
os.waitpid(pid, 0)
sys.stdout.write(out.decode('utf-8', 'replace'))
`

func TestAPasswordTypedOnATerminalIsNotEchoed(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 drives the pseudo-terminal")
	}
	out, err := exec.Command(py, "-I", "-c", ptyDriver, os.Args[0]).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "DONE") {
		t.Fatalf("the helper did not finish: %q", s)
	}
	if strings.Contains(s, "hunter2") {
		t.Fatalf("the password was echoed: %q", s)
	}
	if !strings.Contains(s, "GOT:14 ERR:<nil> ECHO:true") {
		t.Fatalf("%q", s)
	}
}

func TestAPasswordIsNotReadFromAFileThatIsNoTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("secret\n")
	f.Seek(0, 0)
	if IsTerminal(f) {
		t.Fatal("a file is not a terminal")
	}
	if _, err = ReadPassword(context.Background(), f, nil); err != ErrNotTerminal {
		t.Fatalf("%v", err)
	}
}
