package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/port"
)

func newShellCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Manage shell integration",
		Long: `Shell integration is what lets bare "wut" know which command just failed.

The hook writes a small record after each command using shell builtins only —
no process is started, so your prompt does not get slower. WUT reads those
records when you ask it something.`,
	}
	cmd.AddCommand(
		newShellInstallCmd(env, false),
		newShellInstallCmd(env, true),
		newShellStatusCmd(env),
		newShellCaptureCmd(env),
		newShellHookCmd(env),
	)
	return cmd
}

func newShellInstallCmd(env *Env, remove bool) *cobra.Command {
	var (
		shells []string
		dryRun bool
		yes    bool
		alias  string
	)
	use, short := "install", "Add the managed block to your shell startup file"
	if remove {
		use, short = "uninstall", "Remove the managed block"
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := env.App.Deps().Shell
			if mgr == nil {
				return fmt.Errorf("shell integration is unavailable on this platform")
			}

			// Show what will change before changing it. An unattended run is
			// still possible with --yes, but the default is to ask, because
			// this is the one command that edits a file the user owns.
			if !remove && !yes && !dryRun && ttyIsInteractive() {
				preview, err := mgr.Install(port.InstallRequest{Shells: shells, DryRun: true, Alias: alias})
				if err != nil {
					return err
				}
				printShellReport(env, preview, true)
				if !confirm("Write these changes?") {
					return silent(ExitCancelled)
				}
			}

			req := port.InstallRequest{Shells: shells, DryRun: dryRun, Alias: alias}
			var (
				rep port.InstallReport
				err error
			)
			if remove {
				rep, err = mgr.Uninstall(req)
			} else {
				rep, err = mgr.Install(req)
			}
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(rep)
			}
			printShellReport(env, rep, dryRun)
			if !remove && !dryRun {
				printNextSteps(env)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&shells, "shells", nil, "shells to change (default: every one detected)")
	f.BoolVar(&dryRun, "dry-run", false, "show what would change and write nothing")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	if !remove {
		f.StringVar(&alias, "alias", "", "also define a shorter trigger word, e.g. --alias uh")
	}
	return cmd
}

func printShellReport(env *Env, rep port.InstallReport, preview bool) {
	s := env.Style()
	if preview {
		fmt.Fprintf(os.Stdout, "  %s\n\n", s.Bold("These files would change:"))
	}
	for _, c := range rep.Changes {
		switch {
		case c.Err != "":
			fmt.Fprintf(os.Stdout, "  %s %-11s %s\n", s.Red("fail"), c.Shell, s.Dim(c.Err))
		case c.Action == "skipped":
			fmt.Fprintf(os.Stdout, "  %s %-11s %s\n", s.Dim("skip"), c.Shell, s.Dim(c.Message))
		case c.Action == "unchanged":
			fmt.Fprintf(os.Stdout, "  %s %-11s %s\n", s.Dim("  ok"), c.Shell, s.Dim("already up to date"))
		default:
			detail := c.RCFile
			if c.Diff != "" {
				detail += "  " + c.Diff
			}
			fmt.Fprintf(os.Stdout, "  %s %-11s %s\n", s.Green(fmt.Sprintf("%6s", c.Action)), c.Shell, s.Dim(detail))
			if c.Backup != "" {
				fmt.Fprintf(os.Stdout, "         %s\n", s.Grey("backup: "+c.Backup))
			}
		}
	}
	fmt.Fprintln(os.Stdout)
}

func printNextSteps(env *Env) {
	s := env.Style()
	fmt.Fprintf(os.Stdout, "  %s\n", s.Bold("Open a new shell, then try:"))
	fmt.Fprintf(os.Stdout, "    %s   %s\n", s.Cyan("wut"), s.Dim("right after a command fails"))
	fmt.Fprintf(os.Stdout, "    %s   %s\n\n", s.Cyan("wut how do I squash the last 3 commits"), s.Dim("any time"))
}

func newShellStatusCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which shells are set up and what each can see",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := env.App.Deps().Shell
			if mgr == nil {
				return fmt.Errorf("shell integration is unavailable on this platform")
			}
			found, err := mgr.Detect()
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"shells": found})
			}
			s := env.Style()
			fmt.Fprintf(os.Stdout, "  %-12s %-12s %-6s %-10s %s\n",
				s.Bold("shell"), s.Bold("class"), s.Bold("tier"), s.Bold("state"), s.Bold("startup file"))
			for _, d := range found {
				state := "not set up"
				if d.Installed {
					state = "ready"
				}
				if d.Legacy {
					state = "old block"
				}
				marker := " "
				if d.Active {
					marker = "*"
				}
				fmt.Fprintf(os.Stdout, "%s %-12s %-12s %-6s %-10s %s\n",
					marker, d.Name, d.Class, d.Tier, state, s.Dim(d.RCFile))
			}
			fmt.Fprintf(os.Stdout, "\n  %s\n", s.Dim("* the shell you are using now"))
			for _, d := range found {
				if d.Legacy {
					fmt.Fprintf(os.Stdout, "  %s %s still has a block from the old WUT. It is not read — run: wut shell install\n",
						s.Yellow("note:"), d.Name)
				}
			}
			return nil
		},
	}
}

func newShellCaptureCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "capture [off|T0|T0.5|T1]",
		Short: "Choose how much the shell tells WUT",
		Long: `Capture tiers, from least to most:

  off    nothing is recorded
  T0     the command, its exit code, the directory, and how long it took
  T0.5   T0, plus the name of a command that was not found        (default)
  T1     T0.5, plus the error text the command printed

T1 is the only tier that reads output, and it is off by default. Error text
can contain secrets, so it is capped, scrubbed for credentials, and deleted
after capture.retention (24 hours by default).

With no argument, this prints the current tier. "on" and "off" work as you
would expect them to.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := env.App.Config()
			if len(args) == 0 {
				if env.JSON() {
					return render.NewJSON(os.Stdout).Any(map[string]any{
						"tier":      cfg.Capture.Tier,
						"retention": cfg.Capture.Retention.String(),
					})
				}
				fmt.Fprintf(os.Stdout, "  capture tier: %s\n", env.Style().Bold(string(cfg.Capture.Tier)))
				fmt.Fprintf(os.Stdout, "  retention:    %s\n", cfg.Capture.Retention)
				return nil
			}
			// The same code path as `wut config set capture.tier`, so the two
			// spellings cannot drift into different behaviour.
			if err := applyConfig(env, "capture.tier", args[0]); err != nil {
				return err
			}
			if strings.EqualFold(args[0], string(config.TierT1)) && !env.JSON() {
				s := env.Style()
				fmt.Fprintf(os.Stdout, "\n  %s T1 is the only tier that reads command output.\n", s.Yellow("note:"))
				fmt.Fprintf(os.Stdout, "        It is capped, scrubbed for credentials, deleted after %s,\n", cfg.Capture.Retention)
				fmt.Fprintf(os.Stdout, "        and never leaves this machine. %s\n", s.Dim("wut purge removes it now."))
			}
			return nil
		},
	}
}

// newShellHookCmd prints the block without installing it, so a user can read
// exactly what WUT wants to add to their shell before allowing it.
func newShellHookCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "hook <shell>",
		Short: "Print the block that would be installed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := env.App.Deps().Shell
			if mgr == nil {
				return fmt.Errorf("shell integration is unavailable on this platform")
			}
			block, err := mgr.Render(args[0])
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			fmt.Fprint(os.Stdout, block)
			return nil
		},
	}
}
