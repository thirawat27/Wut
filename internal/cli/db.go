package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/port"
)

func newDBCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "db",
		Aliases: []string{"d"},
		Short:   "The local knowledge index",
		Long: `WUT keeps a local copy of the tldr-pages command documentation.

Everything is answered from that copy, so questions work offline and nothing
about what you ask ever leaves the machine. The only time WUT talks to the
network is here.`,
	}
	cmd.AddCommand(newDBSyncCmd(env), newDBStatusCmd(env), newDBClearCmd(env))
	return cmd
}

func newDBSyncCmd(env *Env) *cobra.Command {
	var (
		fromArchive string
		noEmbed     bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Download the tldr pages and rebuild the index",
		Long: `Rebuild the local index from the tldr-pages release.

The existing index is left untouched until the new one is complete, so a sync
that fails halfway leaves you exactly where you were.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := port.SyncOptions{
				FromArchive: fromArchive,
				Embed:       !noEmbed,
			}
			if !env.JSON() {
				st := env.Style()
				opts.Progress = func(step string) {
					fmt.Fprintf(os.Stdout, "  %s %s\n", st.Dim("..."), step)
				}
			}

			res, err := env.App.SyncKnowledge(cmd.Context(), opts)
			if err != nil {
				return &exitError{
					code: ExitError,
					err:  err,
					hint: "the existing index was left untouched",
				}
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(res)
			}
			st := env.Style()
			fmt.Fprintf(os.Stdout, "\n  %s %d pages indexed in %s\n",
				st.Green("done"), res.Pages, res.Took.Round(time.Millisecond))
			fmt.Fprintf(os.Stdout, "  %s\n", st.Dim(fmt.Sprintf("%s  (%s)", res.IndexPath, humanBytes(res.Bytes))))
			fmt.Fprintf(os.Stdout, "  %s\n\n", st.Grey("sha256 "+short(res.Digest)))
			fmt.Fprintf(os.Stdout, "  %s\n", st.Bold("Try:"))
			fmt.Fprintf(os.Stdout, "    %s\n", st.Cyan("wut compress a folder to tar.gz"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&fromArchive, "from-archive", "",
		"build from a local tldr.zip instead of downloading (for offline installs)")
	f.BoolVar(&noEmbed, "no-embed", false,
		"skip the semantic index; keyword search only, and a smaller file")
	return cmd
}

func newDBStatusCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what is in the index",
		RunE: func(cmd *cobra.Command, args []string) error {
			rep := env.App.KnowledgeStatus()
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(rep)
			}
			s := env.Style()
			if !rep.Ready {
				fmt.Fprintf(os.Stdout, "  %s\n", s.Yellow("No index yet."))
				fmt.Fprintf(os.Stdout, "  %s\n", s.Dim("Build it with: wut db sync"))
				return silent(ExitNoKnowledge)
			}
			fmt.Fprintf(os.Stdout, "  %s %d pages, %d examples\n", s.Green("ready"), rep.Pages, rep.Examples)
			if rep.Vectors > 0 {
				fmt.Fprintf(os.Stdout, "         %s\n",
					s.Dim(fmt.Sprintf("%d vectors — natural-language search is on", rep.Vectors)))
			} else {
				fmt.Fprintf(os.Stdout, "         %s\n",
					s.Dim("keyword search only; rebuild without --no-embed for semantic search"))
			}
			fmt.Fprintf(os.Stdout, "         %s  (%s)\n", rep.Path, humanBytes(rep.SizeBytes))
			if !rep.BuiltAt.IsZero() {
				age := s.Dim(fmt.Sprintf("built %s ago", rep.Age.Round(time.Minute)))
				if rep.Stale {
					age = s.Yellow(fmt.Sprintf("built %s ago — refresh with: wut db sync", rep.Age.Round(time.Hour)))
				}
				fmt.Fprintf(os.Stdout, "         %s\n", age)
			}
			return nil
		},
	}
}

func newDBClearCmd(env *Env) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete the index",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := env.App.IndexPath()
			if !yes && ttyIsInteractive() {
				fmt.Fprintf(os.Stdout, "  This deletes %s.\n", path)
				fmt.Fprintf(os.Stdout, "  %s\n", env.Style().Dim("It is derived data; wut db sync rebuilds it."))
				if !confirm("Delete it?") {
					return silent(ExitCancelled)
				}
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			fmt.Fprintln(os.Stdout, "  Index deleted. Rebuild with: wut db sync")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask")
	return cmd
}

func short(digest string) string {
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}
