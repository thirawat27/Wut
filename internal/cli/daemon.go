package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/daemon"
)

// remote returns a daemon client, or nil when the daemon must not be used.
//
// The kill switch is read here rather than from configuration so that
// WUT_NO_DAEMON=1 works with no config file and no daemon installed — it has
// to be the thing you can always reach for when something is wrong.
func (e *Env) remote() *daemon.Client {
	if os.Getenv("WUT_NO_DAEMON") != "" {
		return nil
	}
	dir := e.App.Deps().Dirs.State
	if dir == "" {
		return nil
	}
	return daemon.NewClient(dir)
}

// viaDaemon runs a use case through the daemon, falling back in-process.
//
// The fallback is silent and unconditional. A daemon that is missing, wedged,
// slow, or a version behind must never be worse than not having one, so there
// is no warning, no retry, and no error state a user can get stuck in.
func viaDaemon[T any](e *Env, remote func(*daemon.Client) (T, error), local func() (T, error)) (T, error) {
	if c := e.remote(); c != nil {
		if res, err := remote(c); err == nil {
			return res, nil
		}
	}
	return local()
}

func newDaemonCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "The optional background helper",
		Long: `WUT works fine without this.

The daemon keeps the knowledge index — and, if you have installed one, the
local model — in memory. That turns a question from a second or so into a
fraction of one. Nothing else changes: every feature behaves identically with
the daemon stopped, which is the default.

Turn it off for a single command with WUT_NO_DAEMON=1.`,
	}
	cmd.AddCommand(
		newDaemonStartCmd(env),
		newDaemonRunCmd(env),
		newDaemonStopCmd(env),
		newDaemonStatusCmd(env),
	)
	return cmd
}

// newDaemonRunCmd runs the server in the foreground.
//
// Not hidden away behind the background launcher: when something is wrong with
// the daemon, being able to run exactly what the launcher runs and watch it is
// the difference between a five-minute diagnosis and an afternoon.
func newDaemonRunCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := env.App.Deps()
			srv := daemon.New(env.App, env.Version, deps.Dirs.State, deps.Config.Daemon.IdleTimeout)
			return srv.Serve(cmd.Context())
		},
	}
}

func newDaemonStartCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := daemon.Spawn(env.App.Deps().Dirs.State, 3*time.Second)
			switch {
			case errors.Is(err, daemon.ErrAlreadyRunning):
				fmt.Fprintln(os.Stdout, "  Already running.")
				return nil
			case err != nil:
				return &exitError{
					code: ExitError,
					err:  err,
					hint: "run it in the foreground to see why: wut daemon run",
				}
			}
			fmt.Fprintf(os.Stdout, "  %s daemon running (pid %d)\n", env.Style().Green("ok"), pid)
			return nil
		},
	}
}

func newDaemonStopCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := daemon.NewClient(env.App.Deps().Dirs.State)
			if !client.Available() {
				fmt.Fprintln(os.Stdout, "  Not running.")
				return nil
			}
			if err := client.Stop(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "  Stopped.")
			return nil
		},
	}
}

func newDaemonStatusCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what the daemon is doing",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := daemon.NewClient(env.App.Deps().Dirs.State)
			st, err := client.Status(cmd.Context())
			if err != nil {
				if env.JSON() {
					return render.NewJSON(os.Stdout).Any(map[string]any{"running": false})
				}
				s := env.Style()
				fmt.Fprintf(os.Stdout, "  %s\n", s.Dim("Not running. Everything still works; questions are just slower."))
				fmt.Fprintf(os.Stdout, "  %s\n", s.Dim("Start it with: wut daemon start"))
				return silent(ExitNotFound)
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(st)
			}
			s := env.Style()
			uptime := time.Duration(st.UptimeSec * float64(time.Second)).Round(time.Second)
			fmt.Fprintf(os.Stdout, "  %s pid %d, up %s\n", s.Green("running"), st.PID, uptime)
			fmt.Fprintf(os.Stdout, "          %d requests, %d errors\n", st.Requests, st.Errors)
			if st.Requests > 0 {
				fmt.Fprintf(os.Stdout, "          p50 %.1fms, p95 %.1fms\n", st.P50Millis, st.P95Millis)
			}
			if st.IndexReady {
				fmt.Fprintf(os.Stdout, "          index warm: %d pages\n", st.IndexPages)
			}
			if st.ModelName != "" {
				fmt.Fprintf(os.Stdout, "          model: %s\n", st.ModelName)
			}
			fmt.Fprintf(os.Stdout, "          idle timeout %s\n", st.IdleTimeout)
			return nil
		},
	}
}
