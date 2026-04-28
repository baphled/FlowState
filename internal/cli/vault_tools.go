package cli

import (
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newVaultToolsCmd creates the "vault-tools" command group, parallel to
// "agents", which manages the on-disk Qdrant + LlamaIndex helper scripts
// (sync-vault, query-vault, mcp-vault-server) that originated in the
// dotopencode repo. The scripts are embedded in the FlowState binary so
// a fresh machine can be bootstrapped without cloning a second repo.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with an embedded install subcommand.
//
// Side effects:
//   - Registers the vault-tools install subcommand.
func newVaultToolsCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault-tools",
		Short: "Manage embedded vault-tool scripts (sync-vault, query-vault, mcp-vault-server)",
		Long: "Materialise the Qdrant + LlamaIndex helper scripts shipped inside the FlowState binary " +
			"so a fresh machine can drive vault sync and RAG queries without cloning the dotopencode repo. " +
			"Scripts are written with the executable bit set; the default target lives under " +
			"`~/.local/share/flowstate/vault-tools/` to avoid clobbering the dotopencode source-of-truth at " +
			"`~/.config/opencode/scripts/`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newVaultToolsInstallCmd(getApp))
	return cmd
}

// newVaultToolsInstallCmd creates the "vault-tools install" subcommand.
// It writes the embedded scripts into the configured target directory,
// preserving the executable bit. Existing files whose contents differ
// from the embedded version are skipped by default; pass --force to
// overwrite them.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with --dry-run, --verbose, --force, and
//     --target flags.
//
// Side effects:
//   - Writes scripts to the resolved target directory when not in dry-run.
//   - Writes a per-file report to stdout.
func newVaultToolsInstallCmd(getApp func() *app.App) *cobra.Command {
	var (
		dryRun  bool
		verbose bool
		force   bool
		target  string
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Materialise embedded vault-tool scripts to the target directory",
		Long: "Copy sync-vault, query-vault, and mcp-vault-server out of the FlowState binary into the " +
			"target directory with mode 0755. Default target is `~/.local/share/flowstate/vault-tools/`. " +
			"Use --dry-run to preview, --force to overwrite operator-modified copies, --target to choose " +
			"a different destination (e.g. `~/.config/opencode/scripts` to overwrite the legacy location).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVaultToolsInstall(cmd, getApp(), dryRun, verbose, force, target)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without writing")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Include per-file size deltas in the report")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing scripts whose contents differ from the embedded version")
	cmd.Flags().StringVar(&target, "target", "", "Override the destination directory (default: ~/.local/share/flowstate/vault-tools/)")

	return cmd
}

// runVaultToolsInstall performs the install and prints a report.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App instance (currently unused; accepted
//     so the signature matches the agents-refresh seam and to give the
//     command room to grow into config-driven targets later).
//
// Returns:
//   - nil on success (including "nothing changed").
//   - non-nil error when the install helper fails or output writing fails.
//
// Side effects:
//   - Invokes app.InstallVaultTools (which writes files when not dry-run).
//   - Writes a human-readable report to cmd.OutOrStdout().
func runVaultToolsInstall(cmd *cobra.Command, _ *app.App, dryRun, verbose, force bool, target string) error {
	destDir := target
	if destDir == "" {
		destDir = app.DefaultVaultToolsDir()
	}

	opts := app.VaultToolsInstallOptions{
		DryRun: dryRun,
		Force:  force,
	}

	report, err := app.InstallVaultTools(app.EmbeddedVaultToolsFS(), destDir, opts)
	if err != nil {
		return fmt.Errorf("installing vault tools: %w", err)
	}

	return writeVaultToolsReport(cmd.OutOrStdout(), report, destDir, dryRun, verbose)
}

// writeVaultToolsReport writes the human-readable summary of an install to w.
//
// Expected:
//   - w is a non-nil writer.
//   - report is the VaultToolsReport returned by InstallVaultTools.
//   - destDir is the directory the report applies to (shown in the header).
//
// Returns:
//   - An error if any write fails.
//
// Side effects:
//   - Writes one line per script plus a header and summary.
func writeVaultToolsReport(w io.Writer, report app.VaultToolsReport, destDir string, dryRun, verbose bool) error {
	header := "Installing vault-tool scripts in " + destDir
	if dryRun {
		header += " (dry-run: no files will be written)"
	}
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}

	var created, updated, unchanged, skipped int
	for _, entry := range report {
		if err := writeVaultToolEntry(w, entry, verbose); err != nil {
			return err
		}
		switch entry.Status {
		case app.VaultToolStatusCreated:
			created++
		case app.VaultToolStatusUpdated:
			updated++
		case app.VaultToolStatusUnchanged:
			unchanged++
		case app.VaultToolStatusSkipped:
			skipped++
		}
	}

	summary := fmt.Sprintf("Summary: %d updated, %d created, %d unchanged, %d skipped (%d total)",
		updated, created, unchanged, skipped, len(report))
	_, err := fmt.Fprintln(w, summary)
	return err
}

// writeVaultToolEntry formats a single report line.
//
// Expected:
//   - w is a non-nil writer.
//   - entry is a populated VaultToolsEntry.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes one line to w.
func writeVaultToolEntry(w io.Writer, entry app.VaultToolsEntry, verbose bool) error {
	if verbose {
		_, err := fmt.Fprintf(w, "  %-10s %s (%d -> %d bytes)\n",
			entry.Status, entry.Name, entry.OldSize, entry.NewSize)
		return err
	}
	_, err := fmt.Fprintf(w, "  %-10s %s\n", entry.Status, entry.Name)
	return err
}
