package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thirawat27/wut/internal/adapter/render"
	"github.com/thirawat27/wut/internal/core/config"
)

func newConfigCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Aliases: []string{"c"},
		Short:   "Show, explain, and change configuration",
		Long: `Configuration is a plain YAML file you can also edit by hand.

Every key is documented — "wut config explain" prints what each one does, what
it accepts, and what it defaults to. There is one list of keys behind all of
this, so a key you can set is a key you can read about.`,
	}
	cmd.AddCommand(
		newConfigShowCmd(env),
		newConfigGetCmd(env),
		newConfigSetCmd(env),
		newConfigExplainCmd(env),
		newConfigPathCmd(env),
	)
	return cmd
}

func newConfigShowCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := env.App.Config()
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(cfg)
			}
			s := env.Style()
			fmt.Fprintf(os.Stdout, "  %s %s\n\n", s.Bold("file:"), s.Dim(env.App.Deps().Dirs.ConfigFile()))
			for _, item := range cfg.Settings() {
				value := item.Value
				if value == "" {
					value = s.Dim("(empty)")
				}
				fmt.Fprintf(os.Stdout, "  %-24s %s\n", item.Key, value)
			}
			return nil
		},
	}
}

func newConfigGetCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one value, and nothing else",
		Long: `Print one value on stdout with no decoration, so it can be used in a script:

    if [ "$(wut config get capture.tier)" = "off" ]; then ...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, ok := env.App.Config().Get(args[0])
			if !ok {
				return unknownKey(args[0])
			}
			fmt.Fprintln(os.Stdout, value)
			return nil
		},
	}
}

func newConfigSetCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change a setting and write it to the file",
		Long: `Change one setting.

The value is parsed and the whole configuration validated before anything is
written, so a rejected value leaves the file exactly as it was.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return applyConfig(env, args[0], args[1])
		},
	}
}

// applyConfig is the one path through which a setting changes, so
// `wut config set capture.tier T1` and `wut shell capture T1` cannot behave
// differently.
func applyConfig(env *Env, key, value string) error {
	res, err := env.App.SetConfig(key, value)
	if err != nil {
		return &exitError{code: ExitUsage, err: err, hint: "list the keys with: wut config explain"}
	}
	if env.JSON() {
		return render.NewJSON(os.Stdout).Any(res)
	}
	s := env.Style()
	if !res.Changed {
		fmt.Fprintf(os.Stdout, "  %s %s is already %s\n", s.Dim("  ok"), res.Key, s.Bold(display(res.Value)))
	} else {
		fmt.Fprintf(os.Stdout, "  %s %s  %s -> %s\n",
			s.Green("set"), res.Key, s.Dim(display(res.Previous)), s.Bold(display(res.Value)))
	}
	fmt.Fprintf(os.Stdout, "      %s\n", s.Grey(res.Path))
	return nil
}

func display(v string) string {
	if v == "" {
		return "(empty)"
	}
	return v
}

func newConfigExplainCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "explain [key]",
		Short: "Say what a key does",
		Long: `Explain what a setting does, what it accepts, and what it defaults to.

A setting nobody understands is a setting nobody changes, which is how a
configuration surface grows keys that no longer do anything.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) == 1 {
				prefix = args[0]
			}
			docs := config.KeysMatching(prefix)
			if len(docs) == 0 {
				return unknownKey(prefix)
			}
			if env.JSON() {
				return render.NewJSON(os.Stdout).Any(map[string]any{"keys": docs})
			}
			cfg := env.App.Config()
			s := env.Style()
			for _, d := range docs {
				current, _ := cfg.Get(d.Name)
				fmt.Fprintf(os.Stdout, "  %s\n", s.Bold(d.Name))
				fmt.Fprintf(os.Stdout, "    %s\n", d.What)
				fmt.Fprintf(os.Stdout, "    %s %s   %s %s   %s %s\n\n",
					s.Grey("values:"), d.Values,
					s.Grey("default:"), d.Default,
					s.Grey("now:"), display(current))
			}
			return nil
		},
	}
}

func newConfigPathCmd(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path of the configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stdout, env.App.Deps().Dirs.ConfigFile())
			return nil
		},
	}
}

// unknownKey names every key that does exist. A configuration error that says
// only "unknown key" leaves the user guessing at a list the program already
// has.
func unknownKey(key string) error {
	var names []string
	for _, k := range config.Keys() {
		names = append(names, k.Name)
	}
	return &exitError{
		code: ExitUsage,
		err:  fmt.Errorf("no such configuration key: %s", key),
		hint: "known keys: " + strings.Join(names, ", "),
	}
}
