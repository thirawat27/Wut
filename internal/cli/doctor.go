package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/app"
)

// newDoctorCmd reports what WUT can and cannot see on this machine.
//
// This is the command that makes honest degradation possible. WUT's
// capabilities genuinely vary — by shell, by whether an index is built, by
// whether a model is installed — and a tool that varies silently is one users
// cannot reason about.
func newDoctorCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check this installation and say what WUT can see",
		// Deliberately no "check" alias. Bare `wut <question>` dispatches on
		// the first word, so every alias is a word that can no longer start a
		// question — and "check out a new git branch" silently ran doctor.
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, err := env.App.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(rep)
			}
			printDoctor(env, rep)
			if rep.Problems > 0 {
				return silent(ExitNotFound)
			}
			return nil
		},
	}
}

func printDoctor(env *Env, rep app.DoctorReport) {
	s := env.Style()
	out := os.Stdout

	fmt.Fprintf(out, "%s %s  (%s %s/%s)\n\n", s.Bold("wut"), rep.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)

	for _, sec := range rep.Sections {
		fmt.Fprintf(out, "  %s\n", s.Bold(sec.Name))
		for _, c := range sec.Checks {
			fmt.Fprintf(out, "    %s %-26s %s\n", mark(s, c.Status), c.Name, s.Dim(c.Detail))
			if c.Fix != "" {
				fmt.Fprintf(out, "       %s %s\n", s.Grey("fix:"), s.Grey(c.Fix))
			}
		}
		fmt.Fprintln(out)
	}

	switch rep.Problems {
	case 0:
		fmt.Fprintf(out, "  %s\n", s.Green("Everything checks out."))
	case 1:
		fmt.Fprintf(out, "  %s\n", s.Yellow("1 thing needs attention."))
	default:
		fmt.Fprintf(out, "  %s\n", s.Yellow(fmt.Sprintf("%d things need attention.", rep.Problems)))
	}
}

func mark(s render.Style, status string) string {
	switch status {
	case app.StatusOK:
		if s.Plain() {
			return "ok  "
		}
		return s.Green("ok  ")
	case app.StatusWarn:
		if s.Plain() {
			return "warn"
		}
		return s.Yellow("warn")
	case app.StatusFail:
		if s.Plain() {
			return "fail"
		}
		return s.Red("fail")
	default:
		return s.Dim("--  ")
	}
}
