package shell

import (
	"os/exec"
	"strings"
	"testing"
)

// The generated literal has to be accepted by the interpreter that will read
// it, so it is checked against a real Python rather than by eye.
func TestPyQuoteRoundTripsThroughPython(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("no python on PATH")
	}
	for _, in := range []string{`C:\Users\a\AppData\Local\wut\`, `C:\a"b`, "/home/o'brien/x"} {
		out, err := exec.Command(py, "-c", "import sys;sys.stdout.write("+pyQuote(in)+")").Output()
		if err != nil {
			t.Fatalf("%q -> %s: %v", in, pyQuote(in), err)
		}
		if got := strings.TrimSpace(string(out)); got != in {
			t.Errorf("%q round-tripped as %q", in, got)
		}
	}
}
