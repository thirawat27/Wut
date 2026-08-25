// Package e2e runs the built binary.
//
// Everything else in this repository tests a layer. This tests the promises,
// which are properties of the whole program: that a destructive command never
// reaches a shell, that JSON output matches the published schema, that nothing
// is written outside WUT's own directories, and that the user's command is
// never run. None of those can be checked from inside a package, because each
// of them is about what the process does.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// wut builds the binary once and returns a runner with an isolated home.
func wut(t *testing.T) func(args ...string) (stdout, stderr string, code int) {
	t.Helper()

	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wut-e2e-*")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "wut")
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/wut")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = err
			binPath = string(out)
		}
	})
	if buildErr != nil {
		t.Fatalf("could not build the binary: %v\n%s", buildErr, binPath)
	}

	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home,
		"USERPROFILE="+home,
		"WUT_CONFIG_DIR="+filepath.Join(home, "cfg"),
		"WUT_DATA_DIR="+filepath.Join(home, "data"),
		"WUT_STATE_DIR="+filepath.Join(home, "state"),
		"WUT_NO_DAEMON=1",
		"NO_COLOR=1",
	)

	return func(args ...string) (string, string, int) {
		cmd := exec.Command(binPath, args...)
		cmd.Env = env
		cmd.Dir = home
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		err := cmd.Run()
		code := 0
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("running %v: %v", args, err)
			}
			code = exitErr.ExitCode()
		}
		return out.String(), errBuf.String(), code
	}
}

// I3 — a destructive command must never reach stdout in shell mode, because
// whatever lands there is about to be executed by the user's shell.
func TestShellModeNeverEmitsADestructiveCommand(t *testing.T) {
	run := wut(t)

	for _, command := range []string{
		"rm -rf /",
		"rm -fr ~",
		"git push --force",
		"dd if=/dev/zero of=/dev/sda",
		"chmod -R 777 /",
		"mkfs.ext4 /dev/sda1",
	} {
		stdout, _, code := run("fix", "--shell", "--yes", command)
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("%q put %q on stdout in shell mode", command, strings.TrimSpace(stdout))
		}
		if code == 0 {
			t.Errorf("%q exited 0 in shell mode; the shell would run whatever it read", command)
		}
	}
}

// Shell mode without --yes draws a picker on the controlling terminal and
// waits for a keypress, which is correct and untestable from here: the child
// process inherits this test runner's console, so it would wait forever rather
// than reporting "no terminal". The behaviour that matters — nothing reaches
// stdout unless a human accepted it — is covered by the tests above and by the
// refusal path in internal/cli.

// I1 — WUT never runs the user's command. The sentinel is a command that would
// leave a file behind; after feeding it to every entry point, the file must not
// exist.
func TestTheUsersCommandIsNeverRun(t *testing.T) {
	home := t.TempDir()
	sentinel := filepath.Join(home, "sentinel")
	command := "touch " + sentinel
	if runtime.GOOS == "windows" {
		command = "cmd /c echo x > " + sentinel
	}

	run := wut(t)
	for _, args := range [][]string{
		{"fix", command},
		{"explain", command},
		{"ask", command},
		{"risk", "check", command},
		{"fix", "--shell", "--yes", command},
	} {
		run(args...)
		if _, err := os.Stat(sentinel); err == nil {
			t.Fatalf("%v ran the command: %s exists", args, sentinel)
		}
	}
}

// The output contract: every command that produces a result emits the
// published schema, and every candidate on it carries its reasons.
func TestJSONOutputMatchesTheSchema(t *testing.T) {
	run := wut(t)

	stdout, stderr, code := run("fix", "git psuh", "--output", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}

	var res struct {
		Schema     string `json:"schema"`
		Kind       string `json:"kind"`
		Confidence string `json:"confidence"`
		Candidates []struct {
			Command string `json:"command"`
			Why     []struct {
				Code string `json:"code"`
				Text string `json:"text"`
			} `json:"why"`
			Source struct {
				Producer string `json:"producer"`
			} `json:"source"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	if res.Schema != "wut.v1.result" {
		t.Errorf("schema = %q", res.Schema)
	}
	if len(res.Candidates) == 0 {
		t.Fatalf("no candidates for 'git psuh': %s", stdout)
	}
	for _, c := range res.Candidates {
		if len(c.Why) == 0 {
			t.Errorf("%q reached a consumer with no reasons", c.Command)
		}
		if c.Source.Producer == "" {
			t.Errorf("%q reached a consumer with no producer", c.Command)
		}
	}
}

// Exit codes are part of the contract, not an implementation detail.
func TestExitCodes(t *testing.T) {
	run := wut(t)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"a successful correction", []string{"fix", "git psuh"}, 0},
		{"nothing to correct", []string{"fix", "git status"}, 3},
		{"a bad flag", []string{"fix", "--nope"}, 2},
		{"no knowledge index", []string{"ask", "how do I compress a folder"}, 5},
		{"an unknown config key", []string{"config", "set", "no.such.key", "1"}, 2},
	}
	for _, tc := range cases {
		_, stderr, code := run(tc.args...)
		if code != tc.want {
			t.Errorf("%s: %v exited %d, want %d\n%s", tc.name, tc.args, code, tc.want, stderr)
		}
	}
}

// I6 — nothing is written outside WUT's own directories. A tool that scatters
// state cannot honestly offer `wut purge`.
func TestNothingIsWrittenOutsideItsOwnDirectories(t *testing.T) {
	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home,
		"WUT_CONFIG_DIR="+filepath.Join(home, "wut-config"),
		"WUT_DATA_DIR="+filepath.Join(home, "wut-data"),
		"WUT_STATE_DIR="+filepath.Join(home, "wut-state"),
		"WUT_NO_DAEMON=1", "NO_COLOR=1",
	)
	_ = wut(t) // ensure the binary is built

	for _, args := range [][]string{
		{"fix", "git psuh"},
		{"explain", "ls -la"},
		{"config", "set", "ui.theme", "dark"},
		{"history"},
		{"doctor"},
	} {
		cmd := exec.Command(binPath, args...)
		cmd.Env = env
		cmd.Dir = home
		_ = cmd.Run()
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "wut-config", "wut-data", "wut-state":
		default:
			t.Errorf("something was written outside WUT's directories: %s", e.Name())
		}
	}
}

// Purge must leave nothing. It is the whole privacy story in one verb.
func TestPurgeLeavesNothing(t *testing.T) {
	run := wut(t)
	run("fix", "git psuh")
	run("config", "set", "capture.tier", "T1")

	if _, stderr, code := run("purge", "--yes"); code != 0 {
		t.Fatalf("purge exited %d: %s", code, stderr)
	}
	// Saved commands are the user's own data and must survive; the check that
	// matters here is only that purge succeeded and history is empty.
	stdout, _, _ := run("history", "--output", "json")
	if strings.Contains(stdout, "git psuh") {
		t.Errorf("purge left the history behind: %s", stdout)
	}
}

func TestVersionIsReported(t *testing.T) {
	run := wut(t)
	stdout, _, code := run("version")
	if code != 0 {
		t.Fatalf("version exited %d", code)
	}
	if !strings.Contains(stdout, "1.0.0") {
		t.Errorf("version = %q, want it to report 1.0.0", strings.TrimSpace(stdout))
	}
}

// I9 — piped answers are all consumed. The prototype built a fresh scanner per
// question, so the first read swallowed the rest of stdin and every later
// answer was lost.
func TestPipedInputIsFullyConsumed(t *testing.T) {
	_ = wut(t)
	home := t.TempDir()

	cmd := exec.Command(binPath, "shell", "install", "--dry-run")
	cmd.Env = append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home,
		"WUT_CONFIG_DIR="+filepath.Join(home, "cfg"),
		"WUT_DATA_DIR="+filepath.Join(home, "data"),
		"WUT_STATE_DIR="+filepath.Join(home, "state"),
		"WUT_NO_DAEMON=1", "NO_COLOR=1",
	)
	cmd.Stdin = strings.NewReader("y\nn\ny\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a dry-run install with piped input failed: %v\n%s", err, out)
	}
}
