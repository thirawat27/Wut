package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/core/event"
	"github.com/thirawat27/wut/internal/port"
)

// userStore returns the store cmd/wut constructed.
func (e *Env) userStore() port.UserData {
	return e.App.Deps().UserData
}

func newSaveCmd(env *Env) *cobra.Command {
	var (
		note string
		tags []string
	)
	cmd := &cobra.Command{
		Use:     "save [command]",
		Aliases: []string{"b"},
		Short:   "Keep a command you want to find again",
		Long: `Save a command.

With no argument, saves the last command your shell ran — which is the case
that actually happens: you work something out, it finally runs, and you want
to keep it.

Saved commands live in a plain YAML file you can edit, diff, and keep in a
dotfiles repository. "wut purge" does not touch it.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			command := strings.TrimSpace(strings.Join(args, " "))
			if command == "" {
				last, ok, err := env.App.Deps().Events.Last(cmd.Context(), event.Filter{Session: env.Session()})
				if err != nil {
					return err
				}
				if !ok {
					return &exitError{
						code: ExitNotFound,
						err:  fmt.Errorf("nothing to save"),
						hint: `give it a command: wut save "git log --oneline --graph"`,
					}
				}
				command = last.Raw
			}
			entry, err := env.userStore().Add(command, note, tags)
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(entry)
			}
			s := env.Style()
			fmt.Fprintf(os.Stdout, "  %s %s\n", s.Green("saved"), entry.Command)
			if entry.Note != "" {
				fmt.Fprintf(os.Stdout, "        %s\n", s.Dim(entry.Note))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&note, "note", "m", "", "why you kept it")
	f.StringSliceVar(&tags, "tag", nil, "tags to find it by later")

	cmd.AddCommand(newSavedListCmd(env), newSavedRemoveCmd(env), newSavedPathCmd(env))
	return cmd
}

func newSavedListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "list [filter]",
		Short:   "Show what you have saved",
		Aliases: []string{"ls"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}
			entries, err := env.userStore().List(filter)
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"saved": entries})
			}
			if len(entries) == 0 {
				env.Text().Note("nothing saved yet. Keep one with: wut save")
				return silent(ExitNotFound)
			}
			s := env.Style()
			for _, e := range entries {
				fmt.Fprintf(os.Stdout, "  %s\n", s.Bold(e.Command))
				if e.Note != "" {
					fmt.Fprintf(os.Stdout, "    %s\n", s.Dim(e.Note))
				}
				if len(e.Tags) > 0 {
					fmt.Fprintf(os.Stdout, "    %s\n", s.Grey("#"+strings.Join(e.Tags, " #")))
				}
			}
			return nil
		},
	}
}

func newSavedRemoveCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <command>",
		Short:   "Forget a saved command",
		Aliases: []string{"rm"},
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := env.userStore().Remove(strings.Join(args, " "))
			if err != nil {
				return &exitError{code: ExitNotFound, err: err, hint: "list them with: wut save list"}
			}
			fmt.Fprintf(os.Stdout, "  removed %s\n", entry.Command)
			return nil
		},
	}
}

func newSavedPathCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the file your saved commands live in",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stdout, env.userStore().Path())
			return nil
		},
	}
}

func newAliasCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "alias",
		Aliases: []string{"a"},
		Short:   "Shorthands you define",
		Long: `Define shorthands for commands you type often.

WUT does not install these into your shell. It prints the definitions and you
decide where they go — quietly editing someone's startup file to add aliases
they did not review is exactly the kind of surprise this tool avoids.`,
	}
	cmd.AddCommand(newAliasSetCmd(env), newAliasListCmd(env), newAliasRemoveCmd(env), newAliasShellCmd(env))
	return cmd
}

func newAliasSetCmd(env *Env) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "set <name> <command>",
		Short: "Define a shorthand",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := env.userStore().SetAlias(args[0], strings.Join(args[1:], " "), note)
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			s := env.Style()
			fmt.Fprintf(os.Stdout, "  %s %s -> %s\n", s.Green("alias"), s.Bold(entry.Name), entry.Command)
			fmt.Fprintf(os.Stdout, "  %s\n", s.Dim("add it to your shell with: wut alias shell"))
			return nil
		},
	}
	cmd.Flags().StringVarP(&note, "note", "m", "", "what it is for")
	return cmd
}

func newAliasListCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "Show your shorthands",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			aliases, err := env.userStore().Aliases()
			if err != nil {
				return err
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"aliases": aliases})
			}
			if len(aliases) == 0 {
				env.Text().Note(`nothing defined yet. Try: wut alias set gl "git log --oneline --graph"`)
				return silent(ExitNotFound)
			}
			s := env.Style()
			for _, a := range aliases {
				fmt.Fprintf(os.Stdout, "  %-14s %s\n", s.Bold(a.Name), a.Command)
				if a.Note != "" {
					fmt.Fprintf(os.Stdout, "  %-14s %s\n", "", s.Dim(a.Note))
				}
			}
			return nil
		},
	}
}

func newAliasRemoveCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Short:   "Forget a shorthand",
		Aliases: []string{"rm"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := env.userStore().RemoveAlias(args[0]); err != nil {
				return &exitError{code: ExitNotFound, err: err}
			}
			fmt.Fprintf(os.Stdout, "  removed %s\n", args[0])
			return nil
		},
	}
}

func newAliasShellCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "shell [shell]",
		Short: "Print your shorthands as shell definitions",
		Long: `Print the alias definitions for a shell.

Nothing is written anywhere. Redirect it yourself, or paste it where you want:

    wut alias shell zsh >> ~/.zshrc`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := "sh"
			if len(args) == 1 {
				shell = args[0]
			} else if detected := os.Getenv("WUT_SHELL"); detected != "" {
				shell = detected
			}
			out, err := env.userStore().ShellDefinitions(shell)
			if err != nil {
				return err
			}
			if out == "" {
				env.Text().Note("no aliases defined")
				return silent(ExitNotFound)
			}
			fmt.Fprint(os.Stdout, out)
			return nil
		},
	}
}
