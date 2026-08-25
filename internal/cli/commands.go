package cli

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/app"
	"github.com/thirawat27/wut/internal/core/correct"
	"github.com/thirawat27/wut/internal/core/risk"
	"github.com/thirawat27/wut/internal/daemon"
)

// runAsk answers a natural-language question.
func runAsk(cmd *cobra.Command, env *Env, question string) error {
	req := app.AskRequest{Question: question, Cwd: env.Cwd()}
	res, err := viaDaemon(env,
		func(c *daemon.Client) (app.Result, error) { return c.Ask(cmd.Context(), req) },
		func() (app.Result, error) { return env.App.Ask(cmd.Context(), req) },
	)
	if err != nil {
		return err
	}
	if res.Empty() && !env.App.Deps().Knowledge.Ready() {
		if err := present(env, res, ""); err != nil {
			return silent(ExitNoKnowledge)
		}
		return silent(ExitNoKnowledge)
	}
	return presentInteractive(env, res, "for: "+question)
}

// newAskCmd is the explicit form of a question.
//
// Bare `wut <question>` is the everyday path, but it dispatches on the first
// word, so a question that happens to start with a subcommand name would run
// that subcommand instead. This is the unambiguous escape hatch, and it is
// what scripts should use.
func newAskCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:                "ask <question>",
		Short:              "Ask a question, even if it starts with a command name",
		Example:            "  wut ask check out a new git branch\n  wut ask explain what a symlink is",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAsk(cmd, env, strings.Join(args, " "))
		},
	}
}

func newExplainCmd(env *Env) *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:     "explain <command>",
		Aliases: []string{"x"},
		Short:   "Say what a command does, and what it will change",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.ExplainRequest{
				Command: strings.Join(args, " "),
				Cwd:     env.Cwd(),
				Verbose: verbose,
			}
			res, err := viaDaemon(env,
				func(c *daemon.Client) (app.Result, error) { return c.Explain(cmd.Context(), req) },
				func() (app.Result, error) { return env.App.Explain(cmd.Context(), req) },
			)
			if err != nil {
				return err
			}
			return present(env, res, "")
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "include every example from the page")
	return cmd
}

func newVersionCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]string{
					"version": env.Version,
					"go":      runtime.Version(),
					"os":      runtime.GOOS,
					"arch":    runtime.GOARCH,
				})
			}
			fmt.Fprintf(os.Stdout, "wut %s (%s %s/%s)\n", env.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}

// newRulesCmd exposes the correction rules as data the user can read.
//
// A rule id shows up in every Why line, so being able to look one up is part
// of the "always show why" promise rather than a debugging extra.
func newRulesCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List the correction rules",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List every correction rule",
		RunE: func(cmd *cobra.Command, args []string) error {
			e := correct.New()
			type row struct {
				ID      string `json:"id"`
				Program string `json:"program"`
				Rewrite string `json:"rewrite"`
			}
			var rows []row
			for _, r := range e.Rules().Rules() {
				program := r.Program
				if program == "" {
					program = strings.Join(r.ProgramAnyOf, ", ")
				}
				rows = append(rows, row{ID: r.ID, Program: program, Rewrite: r.Rewrite})
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"rules": rows})
			}
			s := env.Style()
			fmt.Fprintf(os.Stdout, "%d fact rules (fuzzy corrections are built in and not listed here)\n\n", len(rows))
			for _, r := range rows {
				fmt.Fprintf(os.Stdout, "  %s\n    %s\n", s.Bold(r.ID), s.Dim(r.Rewrite))
			}
			return nil
		},
	})
	return cmd
}

// newRiskCmd exposes the risk policy, including a rule lookup, because a
// warning the user cannot investigate is a warning they learn to dismiss.
func newRiskCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "risk",
		Short: "Inspect the safety policy",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List every policy rule",
		RunE: func(cmd *cobra.Command, args []string) error {
			p := risk.Builtin()
			type row struct {
				ID     string `json:"id"`
				Level  string `json:"level"`
				Reason string `json:"reason"`
			}
			var rows []row
			for _, r := range p.Rules() {
				rows = append(rows, row{ID: r.ID, Level: r.Level, Reason: r.Reason})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"rules": rows})
			}
			s := env.Style()
			for _, r := range rows {
				fmt.Fprintf(os.Stdout, "  %-32s %-13s %s\n", s.Bold(r.ID), r.Level, s.Dim(r.Reason))
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "check <command>",
		Short: "Ask what the policy thinks of a command",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := risk.Builtin().AssessString(strings.Join(args, " "))
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(a)
			}
			s := env.Style()
			if a.Safe() {
				fmt.Fprintf(os.Stdout, "  %s nothing in the policy matches this command\n", s.Green("ok"))
				return nil
			}
			fmt.Fprintf(os.Stdout, "  %s %s\n  %s\n", s.Red(strings.ToUpper(a.Level.String())), a.Reason, s.Dim("rule: "+a.Rule))
			return nil
		},
	})
	return cmd
}
