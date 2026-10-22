package cli

import (
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newMemoryToolsCmd creates the "memory-tools" command group, parallel to
// "vault-tools", which manages the on-disk mem0-compatible MCP server
// (mcp-mem0-server.js plus its bash wrapper) that originated in the
// dotopencode repo. The payload is embedded in the FlowState binary so a
// fresh machine can be bootstrapped without cloning a second repo.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with an embedded install subcommand.
//
// Side effects:
//   - Registers the memory-tools install subcommand.
func newMemoryToolsCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory-tools",
		Short: "Manage embedded memory-tool payload (mcp-mem0-server)",
		Long: "Materialise the mem0-compatible MCP server shipped inside the FlowState binary so a fresh " +
			"machine can drive the FlowState memory tools without cloning the dotopencode repo. The wrapper " +
			"is written with the executable bit set; the default target lives under " +
			"`~/.local/share/flowstate/memory-tools/` to avoid clobbering the dotopencode source-of-truth at " +
			"`~/.config/opencode/plugins/lib/dist/`. Node.js (>=18) must be installed separately and " +
			"available on PATH at runtime — only the JavaScript bundle and its launcher are embedded.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newMemoryToolsInstallCmd(getApp))
	return cmd
}

// newMemoryToolsInstallCmd creates the "memory-tools install" subcommand.
// It writes the embedded payload into the configured target directory,
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
//   - Writes payload entries to the resolved target directory when not in
//     dry-run.
//   - Writes a per-file report to stdout.
func newMemoryToolsInstallCmd(getApp func() *app.App) *cobra.Command {
	var (
		dryRun  bool
		verbose bool
		force   bool
		target  string
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Materialise embedded memory-tool payload to the target directory",
		Long: "Copy mcp-mem0-server.js and its bash wrapper out of the FlowState binary into the target " +
			"directory with mode 0755. Default target is `~/.local/share/flowstate/memory-tools/`. " +
			"Use --dry-run to preview, --force to overwrite operator-modified copies, --target to choose " +
			"a different destination. Symlink or PATH-prepend the target after installing so that the " +
			"`mcp-mem0-server` wrapper is discoverable by FlowState's MCP auto-detection.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMemoryToolsInstall(cmd, getApp(), dryRun, verbose, force, target)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without writing")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Include per-file size deltas in the report")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files whose contents differ from the embedded version")
	cmd.Flags().StringVar(&target, "target", "", "Override the destination directory (default: ~/.local/share/flowstate/memory-tools/)")

	return cmd
}

// runMemoryToolsInstall performs the install and prints a report.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App instance (currently unused; accepted
//     so the signature matches the vault-tools install seam and to give
//     the command room to grow into config-driven targets later).
//
// Returns:
//   - nil on success (including "nothing changed").
//   - non-nil error when the install helper fails or output writing fails.
//
// Side effects:
//   - Invokes app.InstallMemoryTools (which writes files when not dry-run).
//   - Writes a human-readable report to cmd.OutOrStdout().
func runMemoryToolsInstall(cmd *cobra.Command, _ *app.App, dryRun, verbose, force bool, target string) error {
	destDir := target
	if destDir == "" {
		destDir = app.DefaultMemoryToolsDir()
	}

	opts := app.MemoryToolsInstallOptions{
		DryRun: dryRun,
		Force:  force,
	}

	report, err := app.InstallMemoryTools(app.EmbeddedMemoryToolsFS(), destDir, opts)
	if err != nil {
		return fmt.Errorf("installing memory tools: %w", err)
	}

	return writeMemoryToolsReport(cmd.OutOrStdout(), report, destDir, dryRun, verbose)
}

// writeMemoryToolsReport writes the human-readable summary of an install to w.
//
// Expected:
//   - w is a non-nil writer.
//   - report is the MemoryToolsReport returned by InstallMemoryTools.
//   - destDir is the directory the report applies to (shown in the header).
//
// Returns:
//   - An error if any write fails.
//
// Side effects:
//   - Writes one line per payload entry plus a header and summary.
func writeMemoryToolsReport(w io.Writer, report app.MemoryToolsReport, destDir string, dryRun, verbose bool) error {
	header := "Installing memory-tool payload in " + destDir
	if dryRun {
		header += " (dry-run: no files will be written)"
	}
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}

	var created, updated, unchanged, skipped int
	for _, entry := range report {
		if err := writeMemoryToolEntry(w, entry, verbose); err != nil {
			return err
		}
		switch entry.Status {
		case app.MemoryToolStatusCreated:
			created++
		case app.MemoryToolStatusUpdated:
			updated++
		case app.MemoryToolStatusUnchanged:
			unchanged++
		case app.MemoryToolStatusSkipped:
			skipped++
		}
	}

	summary := fmt.Sprintf("Summary: %d updated, %d created, %d unchanged, %d skipped (%d total)",
		updated, created, unchanged, skipped, len(report))
	_, err := fmt.Fprintln(w, summary)
	return err
}

// writeMemoryToolEntry formats a single report line.
//
// Expected:
//   - w is a non-nil writer.
//   - entry is a populated MemoryToolsEntry.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes one line to w.
func writeMemoryToolEntry(w io.Writer, entry app.MemoryToolsEntry, verbose bool) error {
	if verbose {
		_, err := fmt.Fprintf(w, "  %-10s %s (%d -> %d bytes)\n",
			entry.Status, entry.Name, entry.OldSize, entry.NewSize)
		return err
	}
	_, err := fmt.Fprintf(w, "  %-10s %s\n", entry.Status, entry.Name)
	return err
}
