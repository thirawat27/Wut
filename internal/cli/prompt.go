package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/thirawat27/wut/internal/platform/tty"
)

// stdin is a single package-level reader shared by every prompt.
//
// This is not premature sharing. The prototype built a fresh bufio.Scanner per
// question, and because a Scanner reads ahead up to 64 KB, the first prompt
// swallowed every buffered line — so `printf 'y\nn\ny\n' | wut init` silently
// ignored answers two onward and used the defaults. One reader, created once,
// is the fix.
var (
	stdinOnce   sync.Once
	stdinReader *bufio.Reader
)

func reader() *bufio.Reader {
	stdinOnce.Do(func() { stdinReader = bufio.NewReader(os.Stdin) })
	return stdinReader
}

// ttyIsInteractive reports whether it makes sense to ask a question.
func ttyIsInteractive() bool { return tty.IsStdinTerminal() && tty.IsStdoutTerminal() }

// confirm asks a yes/no question. A piped answer works; so does no answer at
// all, which is read as "no" rather than as "yes, go ahead".
func confirm(question string) bool {
	fmt.Fprintf(os.Stdout, "  %s [y/N] ", question)
	line, err := reader().ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stdout)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
