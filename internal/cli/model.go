package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
)

func newModelCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "The optional local model",
		Long: `WUT uses two kinds of model, and only one of them is optional.

The one that ships with it is not a download at all: WUT trains a small
semantic index over the tldr pages during "wut db sync". That is what lets you
ask questions in your own words, and it runs on any machine because it is a
lookup table, not a neural network.

The optional one rewrites explanations into plainer language. WUT never asks it
for a command — commands come from the pages — and every flag it mentions is
checked against the source page before you see it. If it writes something that
is not in the page, the whole sentence is thrown away and you get the page's
own wording instead.

WUT does not download models. It uses a local runtime you already have.`,
	}
	cmd.AddCommand(newModelStatusCmd(env), newModelListCmd(env))
	return cmd
}

func newModelStatusCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show which models are in use",
		Aliases: []string{"info"},
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := env.App.Deps()
			ks := deps.Knowledge.Stats()

			type report struct {
				Tier1Ready   bool   `json:"tier1_ready"`
				Tier1Vectors int    `json:"tier1_vectors"`
				Tier2Ready   bool   `json:"tier2_ready"`
				Tier2Name    string `json:"tier2_name,omitempty"`
				Tier2Config  string `json:"tier2_config"`
			}
			rep := report{
				Tier1Ready:   ks.Vectors > 0,
				Tier1Vectors: ks.Vectors,
				Tier2Config:  env.App.Config().Model.Tier2,
			}
			if g := deps.Generator; g != nil && g.Available() {
				rep.Tier2Ready, rep.Tier2Name = true, g.Name()
			}

			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(rep)
			}
			s := env.Style()

			if rep.Tier1Ready {
				fmt.Fprintf(os.Stdout, "  %s  %s\n", s.Green("search "),
					s.Dim(fmt.Sprintf("trained on your index, %d vectors", rep.Tier1Vectors)))
			} else {
				fmt.Fprintf(os.Stdout, "  %s  %s\n", s.Yellow("search "),
					s.Dim("not built — run: wut db sync"))
			}

			switch {
			case rep.Tier2Ready:
				fmt.Fprintf(os.Stdout, "  %s  %s\n", s.Green("wording"), s.Dim(rep.Tier2Name))
			case rep.Tier2Config == "off":
				fmt.Fprintf(os.Stdout, "  %s  %s\n", s.Dim("wording"),
					s.Dim("off — explanations come straight from the page, which is the default"))
			default:
				fmt.Fprintf(os.Stdout, "  %s  %s\n", s.Yellow("wording"),
					s.Dim("configured but no local runtime answered"))
				fmt.Fprintf(os.Stdout, "           %s\n", s.Grey("check it is running, then: wut model list"))
			}
			return nil
		},
	}
}

// modelLister is implemented by backends that can enumerate what they hold.
//
// An optional interface rather than a method on port.Generator: listing is a
// convenience one backend happens to offer, and putting it in the port would
// force every future backend to implement something it may have no notion of.
type modelLister interface {
	Models(ctx context.Context) ([]string, error)
}

func newModelListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the models your local runtime has",
		RunE: func(cmd *cobra.Command, args []string) error {
			lister, ok := env.App.Deps().Generator.(modelLister)
			if !ok {
				return &exitError{
					code: ExitUsage,
					err:  errors.New("this model backend cannot list what it holds"),
					hint: "set model.tier2 to ollama in your config to use a local Ollama",
				}
			}
			models, err := lister.Models(cmd.Context())
			if err != nil {
				return &exitError{
					code: ExitNotFound,
					err:  err,
					hint: "WUT never downloads models; start your local runtime and try again",
				}
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"models": models})
			}
			if len(models) == 0 {
				env.Text().Note("the runtime is up but has no models loaded")
				return silent(ExitNotFound)
			}
			s := env.Style()
			want := env.App.Config().Model.Tier2ID
			for _, m := range models {
				marker := "  "
				if m == want {
					marker = s.Green("* ")
				}
				fmt.Fprintf(os.Stdout, "  %s%s\n", marker, m)
			}
			fmt.Fprintf(os.Stdout, "\n  %s\n", s.Dim("* the one WUT is configured to use (model.tier2_id)"))
			return nil
		},
	}
}
