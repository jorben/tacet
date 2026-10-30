package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		out  string
	}{
		{nil, ExitUsage, "Usage"},
		{[]string{"--help"}, ExitOK, "Usage"},
		{[]string{"version"}, ExitOK, "Tacet"},
		{[]string{"run"}, ExitUsage, "not implemented"},
		{[]string{"nope"}, ExitUsage, "unknown command"},
	} {
		var out, errOut bytes.Buffer
		if got := Main(tc.args, &out, &errOut); got != tc.code {
			t.Errorf("Main(%v) = %d, want %d", tc.args, got, tc.code)
		}
		if !strings.Contains(out.String()+errOut.String(), tc.out) {
			t.Errorf("Main(%v) output %q lacks %q", tc.args, out.String()+errOut.String(), tc.out)
		}
	}
}
