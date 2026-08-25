package app

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/thirawat27/wut/internal/core/config"
	"github.com/thirawat27/wut/internal/platform/tty"
)

// Check statuses.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
	StatusInfo = "info"
)

// Check is one thing that was verified.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Section groups checks.
type Section struct {
	Name   string  `json:"name"`
	Checks []Check `json:"checks"`
}

// DoctorReport is the whole diagnosis.
type DoctorReport struct {
	Version  string    `json:"version"`
	OS       string    `json:"os"`
	Arch     string    `json:"arch"`
	Sections []Section `json:"sections"`
	Problems int       `json:"problems"`
}

// Doctor inspects this installation.
//
// Every check answers "what can WUT see, and what would change that". A check
// that reports a problem without a fix is a check that wastes the reader's
// time, so Fix is filled in wherever there is one.
func (a *App) Doctor(ctx context.Context) (DoctorReport, error) {
	rep := DoctorReport{Version: a.deps.Version, OS: runtime.GOOS, Arch: runtime.GOARCH}

	rep.Sections = append(rep.Sections,
		a.doctorPaths(),
		a.doctorCapture(ctx),
		a.doctorKnowledge(),
		a.doctorModels(),
		a.doctorTerminal(),
	)

	for _, s := range rep.Sections {
		for _, c := range s.Checks {
			if c.Status == StatusWarn || c.Status == StatusFail {
				rep.Problems++
			}
		}
	}
	return rep, nil
}

func (a *App) doctorPaths() Section {
	d := a.deps.Dirs
	sec := Section{Name: "Locations"}
	for _, p := range []struct{ name, path string }{
		{"config", d.Config},
		{"data", d.Data},
		{"state", d.State},
		{"cache", d.Cache},
	} {
		status, detail := StatusOK, p.path
		if _, err := os.Stat(p.path); err != nil {
			status, detail = StatusInfo, p.path+"  (not created yet)"
		}
		sec.Checks = append(sec.Checks, Check{Name: p.name, Status: status, Detail: detail})
	}
	return sec
}

func (a *App) doctorCapture(ctx context.Context) Section {
	sec := Section{Name: "What WUT can see"}
	cfg := a.deps.Config

	tierCheck := Check{Name: "capture tier", Detail: string(cfg.Capture.Tier)}
	switch cfg.Capture.Tier {
	case config.TierOff:
		tierCheck.Status = StatusWarn
		tierCheck.Detail = "off — bare `wut` cannot know what just failed"
		tierCheck.Fix = "wut shell capture on"
	case config.TierT0:
		tierCheck.Status = StatusOK
		tierCheck.Detail = "T0 — command, exit code, directory, duration"
	case config.TierT05:
		tierCheck.Status = StatusOK
		tierCheck.Detail = "T0.5 — T0 plus command-not-found"
	case config.TierT1:
		tierCheck.Status = StatusOK
		tierCheck.Detail = "T1 — T0.5 plus captured error output"
	}
	sec.Checks = append(sec.Checks, tierCheck)

	stats, err := a.deps.Events.Stats(ctx)
	switch {
	case err != nil:
		sec.Checks = append(sec.Checks, Check{
			Name: "event log", Status: StatusFail, Detail: err.Error(),
		})
	case stats.Events == 0:
		sec.Checks = append(sec.Checks, Check{
			Name:   "event log",
			Status: StatusWarn,
			Detail: "no events recorded yet",
			Fix:    "wut shell install, then open a new shell",
		})
	default:
		sec.Checks = append(sec.Checks, Check{
			Name:   "event log",
			Status: StatusOK,
			Detail: fmt.Sprintf("%d events, newest %s ago", stats.Events, ago(stats.Newest)),
		})
	}

	// The fact probe is what makes fact-driven corrections possible. Say so
	// plainly, because it is also the part users are most likely to want to
	// understand before trusting it.
	sec.Checks = append(sec.Checks, Check{
		Name:   "project facts",
		Status: StatusOK,
		Detail: "read-only, allowlisted: git rev-parse, git remote, git branch, package.json, Makefile",
	})
	return sec
}

func (a *App) doctorKnowledge() Section {
	sec := Section{Name: "Knowledge"}
	st := a.deps.Knowledge.Stats()
	if !st.Ready {
		sec.Checks = append(sec.Checks, Check{
			Name:   "tldr index",
			Status: StatusWarn,
			Detail: "not built — natural-language questions and explanations are limited",
			Fix:    "wut db sync",
		})
		return sec
	}
	detail := fmt.Sprintf("%d pages, %d examples", st.Pages, st.Examples)
	if st.Release != "" {
		detail += ", release " + st.Release
	}
	if !st.BuiltAt.IsZero() {
		detail += ", built " + ago(st.BuiltAt) + " ago"
	}
	sec.Checks = append(sec.Checks, Check{Name: "tldr index", Status: StatusOK, Detail: detail})
	if st.Vectors > 0 {
		sec.Checks = append(sec.Checks, Check{
			Name: "semantic index", Status: StatusOK,
			Detail: fmt.Sprintf("%d vectors", st.Vectors),
		})
	} else {
		sec.Checks = append(sec.Checks, Check{
			Name: "semantic index", Status: StatusInfo,
			Detail: "not built — questions fall back to keyword search",
			Fix:    "wut db sync --embed",
		})
	}
	return sec
}

func (a *App) doctorModels() Section {
	sec := Section{Name: "Local model"}

	if a.deps.Embedder != nil && a.deps.Embedder.Dimensions() > 0 {
		sec.Checks = append(sec.Checks, Check{
			Name:   "tier 1 (search)",
			Status: StatusOK,
			Detail: fmt.Sprintf("%s, %d dimensions", a.deps.Embedder.ID(), a.deps.Embedder.Dimensions()),
		})
	} else {
		sec.Checks = append(sec.Checks, Check{
			Name: "tier 1 (search)", Status: StatusInfo,
			Detail: "not loaded — keyword search only",
			Fix:    "wut db sync --embed",
		})
	}

	if a.deps.Generator != nil && a.deps.Generator.Available() {
		sec.Checks = append(sec.Checks, Check{
			Name: "tier 2 (wording)", Status: StatusOK,
			Detail: a.deps.Generator.Name() + " — explanations may be rephrased, and every flag is checked against the page",
		})
	} else {
		sec.Checks = append(sec.Checks, Check{
			Name: "tier 2 (wording)", Status: StatusInfo,
			Detail: "not installed — explanations come from the page directly, which is the default",
			Fix:    "wut model install",
		})
	}
	return sec
}

func (a *App) doctorTerminal() Section {
	sec := Section{Name: "Terminal"}
	if tty.Available() {
		sec.Checks = append(sec.Checks, Check{
			Name: "controlling terminal", Status: StatusOK,
			Detail: "available — the picker can draw without touching stdout",
		})
	} else {
		sec.Checks = append(sec.Checks, Check{
			Name: "controlling terminal", Status: StatusInfo,
			Detail: "none — WUT will print candidates instead of offering a picker",
		})
	}
	sec.Checks = append(sec.Checks, Check{
		Name: "stdout", Status: StatusInfo, Detail: describeStdout(),
	})
	return sec
}

func describeStdout() string {
	if tty.IsStdoutTerminal() {
		return fmt.Sprintf("terminal, %d columns", tty.StdoutWidth())
	}
	return "redirected — output will be plain"
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
