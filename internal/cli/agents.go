package cli

import (
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newAgentsCmd creates the "agents" command group for managing on-disk agent
// manifests. Distinct from `agent` (singular) which inspects the in-memory
// registry; this group manipulates the files in AgentDir.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with an embedded refresh subcommand.
//
// Side effects:
//   - Registers the agents refresh subcommand.
func newAgentsCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage on-disk agent manifests",
		Long:  "Manage the agent manifest files in the FlowState agents directory (separate from the in-memory registry surfaced by `agent`).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAgentsRefreshCmd(getApp))
	return cmd
}

// newAgentsRefreshCmd creates the "agents refresh" subcommand. The command
// force-refreshes agent manifests from the binary's embedded set into the
// configured agents directory, overwriting stale copies so users pick up
// upstream manifest edits after updating FlowState.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with --dry-run, --verbose, and --agent flags.
//
// Side effects:
//   - Writes updated manifests to app.AgentsDir() when not in dry-run.
//   - Writes a per-file report to stdout.
func newAgentsRefreshCmd(getApp func() *app.App) *cobra.Command {
	var (
		dryRun    bool
		verbose   bool
		onlyAgent string
	)

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Force-refresh agent manifests from the embedded set",
		Long: "Overwrite on-disk agent manifests with the versions embedded in the FlowState binary. " +
			"Use --dry-run to preview changes without writing. Use --agent to refresh a single manifest by ID.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentsRefresh(cmd, getApp(), dryRun, verbose, onlyAgent)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without writing")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Include per-file size deltas in the report")
	cmd.Flags().StringVar(&onlyAgent, "agent", "", "Refresh only the manifest with this ID (filename without .md)")

	return cmd
}

// runAgentsRefresh performs the refresh and prints a report.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App instance with a resolved AgentsDir.
//
// Returns:
//   - nil on success (including "nothing changed").
//   - non-nil error when the refresh helper fails or output writing fails; the
//     returned error causes Cobra to exit non-zero.
//
// Side effects:
//   - Invokes app.RefreshAgentManifests (which writes files when not dry-run).
//   - Writes a human-readable report to cmd.OutOrStdout().
func runAgentsRefresh(cmd *cobra.Command, application *app.App, dryRun, verbose bool, onlyAgent string) error {
	destDir := application.AgentsDir()
	opts := app.RefreshOptions{
		DryRun:    dryRun,
		OnlyAgent: onlyAgent,
	}

	report, err := app.RefreshAgentManifests(app.EmbeddedAgentsFS(), destDir, opts)
	if err != nil {
		return fmt.Errorf("refreshing agent manifests: %w", err)
	}

	return writeRefreshReport(cmd.OutOrStdout(), report, destDir, dryRun, verbose)
}

// writeRefreshReport writes the human-readable summary of a refresh to w.
//
// Expected:
//   - w is a non-nil writer.
//   - report is the RefreshReport returned by RefreshAgentManifests.
//   - destDir is the directory the report applies to (shown in the header).
//
// Returns:
//   - An error if any write fails.
//
// Side effects:
//   - Writes one line per manifest plus a header and summary.
func writeRefreshReport(w io.Writer, report app.RefreshReport, destDir string, dryRun, verbose bool) error {
	header := "Refreshing manifests in " + destDir
	if dryRun {
		header += " (dry-run: no files will be written)"
	}
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}

	var created, updated, unchanged int
	for _, entry := range report {
		if err := writeRefreshEntry(w, entry, verbose); err != nil {
			return err
		}
		switch entry.Status {
		case app.RefreshStatusCreated:
			created++
		case app.RefreshStatusUpdated:
			updated++
		case app.RefreshStatusUnchanged:
			unchanged++
		}
	}

	summary := fmt.Sprintf("Summary: %d updated, %d created, %d unchanged (%d total)",
		updated, created, unchanged, len(report))
	_, err := fmt.Fprintln(w, summary)
	return err
}

// writeRefreshEntry formats a single report line.
//
// Expected:
//   - w is a non-nil writer.
//   - entry is a populated RefreshEntry.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes one line to w.
func writeRefreshEntry(w io.Writer, entry app.RefreshEntry, verbose bool) error {
	if verbose {
		_, err := fmt.Fprintf(w, "  %-10s %s (%d -> %d bytes)\n",
			entry.Status, entry.Name, entry.OldSize, entry.NewSize)
		return err
	}
	_, err := fmt.Fprintf(w, "  %-10s %s\n", entry.Status, entry.Name)
	return err
}
