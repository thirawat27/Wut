// Package cmd provides CLI commands for WUT
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"wut/internal/shell"

	"github.com/spf13/cobra"
)

// installCmd represents the install command
var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install shell integration",
	Long: `Install WUT shell integration with key bindings.

This command sets up key bindings for your shell to quickly access WUT:
- Ctrl+Space: Open WUT TUI
- Ctrl+G: Open WUT with current command line
- oops: Show WUT's correction for the last command

The integration is intentionally minimal and non-invasive: it does not replace
your shell's command-not-found handler or prompt. A backup of your shell config
is created automatically before any changes are made.

With no arguments, 'wut install' installs integration for the detected active
shell only and asks for confirmation first. Use --all only when you want to
install for every detected shell.`,
	Example: `  wut install                      # Install for detected shell
  wut install --shell bash         # Install for bash
  wut install --shell zsh --dry-run # Preview changes for zsh
  wut install --all --yes          # Install for all detected shells
  wut install --uninstall          # Remove from detected shell`,
	RunE: runInstall,
}

var (
	installAll       bool
	installUninstall bool
	installShell     string
	installDryRun    bool
	installYes       bool
	installBackup    bool
)

func init() {
	rootCmd.AddCommand(installCmd)

	installCmd.Flags().BoolVarP(&installAll, "all", "a", false, "install for all detected shells")
	installCmd.Flags().BoolVarP(&installUninstall, "uninstall", "u", false, "uninstall shell integration")
	installCmd.Flags().StringVarP(&installShell, "shell", "s", "", "target shell")
	installCmd.Flags().BoolVar(&installDryRun, "dry-run", false, "show what would change without modifying anything")
	installCmd.Flags().BoolVarP(&installYes, "yes", "y", false, "skip confirmation prompts")
	installCmd.Flags().BoolVar(&installBackup, "backup", true, "create a timestamped backup before modifying shell configs")
}

func runInstall(cmd *cobra.Command, args []string) error {
	if installUninstall {
		return runUninstall()
	}

	if installShell == "" && !installAll {
		// Default to the active shell only, with confirmation, so the command
		// stays short and convenient without touching every installed shell.
		installShell = detectShell()
		if installShell == "" {
			return fmt.Errorf("could not detect shell, please specify with --shell")
		}
		fmt.Printf("Detected shell: %s\n", installShell)
	}

	installer := shell.NewInstaller()
	installer.DryRun = installDryRun
	installer.Backup = installBackup

	if installAll {
		return installAllShells(installer)
	}

	return installSingleShell(installer, installShell)
}

func runUninstall() error {
	installer := shell.NewInstaller()
	installer.DryRun = installDryRun
	installer.Backup = installBackup

	if installShell == "" && !installAll {
		installShell = detectShell()
		if installShell == "" {
			return fmt.Errorf("could not detect shell, please specify with --shell")
		}
		fmt.Printf("Detected shell: %s\n", installShell)
	}

	if installAll {
		return uninstallAllShells(installer)
	}

	return uninstallSingleShell(installer, installShell)
}

func installSingleShell(installer *shell.Installer, sh string) error {
	sh = normalizeInstallShell(sh)
	if !shell.SupportsInstall(sh) {
		return fmt.Errorf("live integration is not implemented for %s yet; installable shells: %s", sh, strings.Join(shell.IntegrationShells(), ", "))
	}

	configFile, err := shell.GetConfigFile(sh)
	if err != nil {
		return err
	}

	if !installYes && !installDryRun {
		fmt.Printf("WUT will add a small integration block to:\n  %s\n", configFile)
		if installBackup {
			fmt.Println("A timestamped backup will be created first.")
		}
		if !confirm("Continue? [y/N]: ") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	fmt.Printf("Installing WUT integration for %s...\n", sh)
	plan, err := installer.Install(sh)
	if err != nil {
		if err.Error() == "already installed" {
			fmt.Println("WUT integration is already installed")
			return nil
		}
		return err
	}

	if installDryRun {
		fmt.Println("Dry run — no files were modified.")
		printPlan(plan)
		return nil
	}

	fmt.Println("Successfully installed!")
	printPlan(plan)
	fmt.Println()
	fmt.Println("Key bindings:")
	fmt.Println("  • Ctrl+Space - Open WUT TUI")
	fmt.Println("  • Ctrl+G     - Open WUT with current command")
	fmt.Println("  • oops       - Show WUT's correction for the last command")
	fmt.Println()
	if reloadCmd := shell.GetReloadCommand(sh, plan.ConfigFile); reloadCmd != "" {
		fmt.Printf("Please restart your shell or run: %s\n", reloadCmd)
	} else {
		fmt.Println("Please restart your shell to load the integration.")
	}

	return runPostInstallHistoryImport()
}

func uninstallSingleShell(installer *shell.Installer, sh string) error {
	sh = normalizeInstallShell(sh)

	configFile, err := shell.GetConfigFile(sh)
	if err != nil {
		return err
	}

	if !installYes && !installDryRun {
		fmt.Printf("WUT will remove its integration block from:\n  %s\n", configFile)
		if installBackup {
			fmt.Println("The most recent backup will be restored if one exists.")
		}
		if !confirm("Continue? [y/N]: ") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	fmt.Printf("Removing WUT integration from %s...\n", sh)
	plan, err := installer.Uninstall(sh)
	if err != nil {
		return err
	}

	if installDryRun {
		fmt.Println("Dry run — no files were modified.")
		printPlan(plan)
		return nil
	}

	fmt.Println("Successfully uninstalled!")
	printPlan(plan)
	if reloadCmd := shell.GetReloadCommand(sh, plan.ConfigFile); reloadCmd != "" {
		fmt.Printf("Please restart your shell or run: %s\n", reloadCmd)
	} else {
		fmt.Println("Please restart your shell to unload the integration.")
	}
	return nil
}

func installAllShells(installer *shell.Installer) error {
	shells := detectAllShells()
	if len(shells) == 0 {
		return fmt.Errorf("no shells detected")
	}

	if !installYes && !installDryRun {
		fmt.Printf("WUT will install integration for: %s\n", strings.Join(shells, ", "))
		if installBackup {
			fmt.Println("A timestamped backup will be created for each config file.")
		}
		if !confirm("Continue? [y/N]: ") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	for _, sh := range shells {
		if _, err := installer.Install(sh); err != nil {
			fmt.Printf("Failed to install for %s: %v\n", sh, err)
		} else if installDryRun {
			fmt.Printf("Would install integration for %s\n", sh)
		} else {
			fmt.Printf("Installed integration for %s\n", sh)
		}
		fmt.Println()
	}

	if installDryRun {
		return nil
	}
	return runPostInstallHistoryImport()
}

func uninstallAllShells(installer *shell.Installer) error {
	shells := detectAllShells()
	if len(shells) == 0 {
		return fmt.Errorf("no shells detected")
	}

	if !installYes && !installDryRun {
		fmt.Printf("WUT will uninstall integration from: %s\n", strings.Join(shells, ", "))
		if installBackup {
			fmt.Println("The most recent backup will be restored for each config file.")
		}
		if !confirm("Continue? [y/N]: ") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	for _, sh := range shells {
		if _, err := installer.Uninstall(sh); err != nil {
			fmt.Printf("Failed to uninstall for %s: %v\n", sh, err)
		} else if installDryRun {
			fmt.Printf("Would uninstall integration from %s\n", sh)
		} else {
			fmt.Printf("Uninstalled integration from %s\n", sh)
		}
	}
	return nil
}

func detectShell() string {
	return normalizeInstallShell(shell.DetectPreferredInstallShell())
}

func detectAllShells() []string {
	return shell.DetectInstallableShells()
}

func normalizeInstallShell(sh string) string {
	return shell.CanonicalName(sh)
}

func runPostInstallHistoryImport() error {
	importCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	summary, err := bootstrapShellHistoryImport(importCtx)
	if err != nil {
		fmt.Printf("Shell history import skipped: %v\n", err)
		return nil
	}

	switch {
	case summary.imported > 0:
		fmt.Printf("Imported %d history entries from %d shell sources\n", summary.imported, len(summary.sources))
	case len(summary.sources) > 0:
		fmt.Printf("Scanned %d shell history sources; no new commands to import\n", len(summary.sources))
	default:
		fmt.Println("No shell history sources detected")
	}

	return nil
}

func confirm(prompt string) bool {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
	return false
}

func printPlan(plan *shell.InstallPlan) {
	if plan == nil {
		return
	}
	if plan.ConfigFile != "" {
		fmt.Printf("  Config file: %s\n", plan.ConfigFile)
	}
	if plan.BackupFile != "" {
		fmt.Printf("  Backup file: %s\n", plan.BackupFile)
	}
	for _, action := range plan.Actions {
		fmt.Printf("  • %s\n", action)
	}
}
