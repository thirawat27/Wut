// Package cli is the command tree, and nothing else.
//
// It receives an already-wired App. It does not construct adapters, does not
// read files, and does not decide anything a use case could decide — the whole
// point of the layer is that swapping the CLI for the daemon changes only this
// package.
//
// Exit codes are part of the contract, not an implementation detail:
//
//	0    a candidate was produced (and accepted, in shell mode)
//	1    internal error
//	2    usage error
//	3    nothing found, or shell mode with no controlling terminal
//	4    refused: the only candidate was destructive
//	5    knowledge index missing or damaged
//	130  cancelled by the user
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/daemon"
	"github.com/thirawat27/wut/internal/platform/tty"
)

// Exit codes.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitNotFound    = 3
	ExitRefused     = 4
	ExitNoKnowledge = 5
	ExitCancelled   = 130
)

// Env carries what every command needs.
type Env struct {
	App     *app.App
	Version string

	// Global flags.
	outputFlag  string
	cwdFlag     string
	sessionFlag string
	noColor     bool
	verbose     bool
	quiet       bool
}

// Output resolves the rendering mode: flag, then config, then the environment.
//
// CI gets JSON by default. A tool that prints a picker into a build log is
// useless, and one that prints styled escapes into one is worse.
func (e *Env) Output() config.Output {
	if e.outputFlag != "" {
		return config.Output(strings.ToLower(e.outputFlag))
	}
	if os.Getenv("CI") != "" && !tty.IsStdoutTerminal() {
		return config.OutputJSON
	}
	return e.App.Config().UI.Output
}

// JSON reports whether output should be machine-readable.
func (e *Env) JSON() bool { return e.Output() == config.OutputJSON }

// Cwd is the directory to read facts from.
func (e *Env) Cwd() string {
	if e.cwdFlag != "" {
		return e.cwdFlag
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// Session is the shell session id, supplied by the hook.
func (e *Env) Session() string {
	if e.sessionFlag != "" {
		return e.sessionFlag
	}
	return os.Getenv("WUT_SESSION")
}

// Style builds the renderer style for stdout.
func (e *Env) Style() render.Style {
	colorable := tty.IsStdoutTerminal() && !e.noColor
	return render.NewStyle(colorable, tty.StdoutWidth())
}

// Text builds a text renderer on stdout.
func (e *Env) Text() *render.Text {
	t := render.NewText(os.Stdout, e.Style())
	t.ShowWhy = !e.quiet
	return t
}

// Root builds the command tree.
func Root(a *app.App, version string) (*cobra.Command, *Env) {
	env := &Env{App: a, Version: version}

	root := &cobra.Command{
		Use:   "wut [question]",
		Short: "The terminal answers.",
		Long: `WUT answers three questions, and they are all the same question at
different moments:

  wut                     that just failed — what did I mean?
  wut <question>          how do I do this?
  wut explain <command>   what will this do to my machine?

If your question starts with a word that is also a subcommand, say "wut ask"
instead: wut ask check out a new branch

WUT never runs your command. It shows what it would run, says why, and lets
you decide.`,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Fold any new session records into the event log before anything
		// reads it. Failures here are deliberately silent: a corrupt record
		// file must not stop WUT from answering a question that did not need
		// it, and the user finds out through `wut doctor` instead.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			_, _ = a.Deps().Events.Ingest(cmd.Context())
		},
		// Bare `wut` and `wut <free text>` both land here: cobra only reaches
		// RunE when args[0] is not a known subcommand, which is exactly the
		// dispatch we want.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBare(cmd, env, args)
		},
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&env.outputFlag, "output", "o", "", "output format: text|json")
	pf.StringVar(&env.cwdFlag, "cwd", "", "directory to read project facts from")
	pf.StringVar(&env.sessionFlag, "session", "", "shell session id (set by the shell hook)")
	pf.BoolVar(&env.noColor, "no-color", false, "disable colour")
	pf.BoolVarP(&env.verbose, "verbose", "v", false, "explain what WUT is doing")
	pf.BoolVarP(&env.quiet, "quiet", "q", false, "hide the reasons under each candidate")

	root.AddCommand(
		newAskCmd(env),
		newFixCmd(env),
		newExplainCmd(env),
		newDoctorCmd(env),
		newVersionCmd(env),
		newConfigCmd(env),
		newRulesCmd(env),
		newRiskCmd(env),
		newShellCmd(env),
		newDBCmd(env),
		newHistoryCmd(env),
		newPurgeCmd(env),
		newDaemonCmd(env),
		newModelCmd(env),
		newUICmd(env),
		newSaveCmd(env),
		newAliasCmd(env),
	)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	return root, env
}

// runBare is the context-aware entry point.
//
// This is the whole of the "one thing to remember" promise: three letters, and
// WUT does the most useful thing available given what just happened.
func runBare(cmd *cobra.Command, env *Env, args []string) error {
	question := strings.TrimSpace(strings.Join(args, " "))
	if question != "" {
		return runAsk(cmd, env, question)
	}

	// No question: correct the last failure if there is one.
	req := app.FixRequest{Cwd: env.Cwd(), Session: env.Session()}
	res, err := viaDaemon(env,
		func(c *daemon.Client) (app.Result, error) { return c.Fix(cmd.Context(), req) },
		func() (app.Result, error) { return env.App.Fix(cmd.Context(), req) },
	)
	if err != nil {
		return err
	}
	if !res.Empty() {
		return present(env, res, "the last command failed")
	}

	// Nothing failed recently. On a terminal, ask what they want; otherwise
	// print help and exit cleanly, because a bare `wut` in a script should not
	// look like an error.
	if !tty.IsStdinTerminal() {
		return cmd.Help()
	}
	fmt.Fprintln(os.Stdout, env.Style().Dim("Nothing has failed recently. Ask me something:"))
	fmt.Fprintln(os.Stdout, env.Style().Dim("  wut compress a folder to tar.gz"))
	for _, n := range res.Notes {
		env.Text().Note("%s", n)
	}
	return nil
}
