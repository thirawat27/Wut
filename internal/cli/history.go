package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/core/event"
)

func newHistoryCmd(env *Env) *cobra.Command {
	var (
		limit      int
		failedOnly bool
		stats      bool
	)
	cmd := &cobra.Command{
		Use:     "history",
		Aliases: []string{"h"},
		Short:   "What your shell has told WUT",
		RunE: func(cmd *cobra.Command, args []string) error {
			store := env.App.Deps().Events
			if stats {
				return printHistoryStats(cmd, env)
			}
			events, err := store.Recent(cmd.Context(), event.Filter{
				FailedOnly: failedOnly,
				Limit:      limit,
			})
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"events": events})
			}
			if len(events) == 0 {
				env.Text().Note("nothing recorded yet. Set it up with: wut shell install")
				return silent(ExitNotFound)
			}
			s := env.Style()
			for _, e := range events {
				status := s.Green("  ok")
				if e.Failed() {
					status = s.Red(fmt.Sprintf("%4d", e.ExitCode))
				}
				fmt.Fprintf(os.Stdout, "  %s %s  %s\n",
					status, s.Dim(e.At.Format("15:04:05")), e.Raw)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVarP(&limit, "limit", "n", 25, "how many to show")
	f.BoolVar(&failedOnly, "failed", false, "only commands that failed")
	f.BoolVar(&stats, "stats", false, "summarise instead of listing")
	return cmd
}

func printHistoryStats(cmd *cobra.Command, env *Env) error {
	store := env.App.Deps().Events
	st, err := store.Stats(cmd.Context())
	if err != nil {
		return err
	}
	events, err := store.Recent(cmd.Context(), event.Filter{})
	if err != nil {
		return err
	}

	byProgram := map[string]int{}
	failures := map[string]int{}
	for _, e := range events {
		prog := firstField(e.Raw)
		if prog == "" {
			continue
		}
		byProgram[prog]++
		if e.Failed() {
			failures[prog]++
		}
	}

	type row struct {
		Program  string  `json:"program"`
		Runs     int     `json:"runs"`
		Failures int     `json:"failures"`
		Rate     float64 `json:"failure_rate"`
	}
	var rows []row
	for prog, n := range byProgram {
		r := row{Program: prog, Runs: n, Failures: failures[prog]}
		if n > 0 {
			r.Rate = float64(failures[prog]) / float64(n)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Runs > rows[j].Runs })
	if len(rows) > 15 {
		rows = rows[:15]
	}

	if env.JSON() {
		return render.NewJSON(os.Stdout).Any(map[string]any{"stats": st, "programs": rows})
	}

	s := env.Style()
	fmt.Fprintf(os.Stdout, "  %d events, %d with captured output, %s on disk\n",
		st.Events, st.WithOutput, humanBytes(st.SizeBytes))
	fmt.Fprintf(os.Stdout, "  capture tier %s, output kept for %.0fh\n\n", st.CaptureTier, st.RetentionHrs)
	if len(rows) == 0 {
		return nil
	}
	fmt.Fprintf(os.Stdout, "  %-16s %6s %9s\n", s.Bold("command"), s.Bold("runs"), s.Bold("failed"))
	for _, r := range rows {
		failed := fmt.Sprintf("%d (%.0f%%)", r.Failures, r.Rate*100)
		if r.Failures == 0 {
			failed = "0"
		}
		fmt.Fprintf(os.Stdout, "  %-16s %6d %9s\n", r.Program, r.Runs, failed)
	}
	return nil
}

// newPurgeCmd deletes everything WUT has recorded.
//
// One command, no submenus. Privacy that takes six clicks is privacy nobody
// uses, and it should always be obvious how to make WUT forget.
func newPurgeCmd(env *Env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete everything WUT has recorded about you",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := env.App.Deps().Events.Stats(cmd.Context())
			if err != nil {
				return err
			}
			if !yes && tty_IsInteractive() {
				fmt.Fprintf(os.Stdout, "  This deletes %d events and every session record.\n", st.Events)
				if !confirm("Delete them?") {
					return silent(ExitCancelled)
				}
			}
			n, err := env.App.Deps().Events.Purge(cmd.Context())
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"deleted": n})
			}
			fmt.Fprintf(os.Stdout, "  Deleted %d events and every session record.\n", n)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	return cmd
}

func firstField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	name := f[0]
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
}
