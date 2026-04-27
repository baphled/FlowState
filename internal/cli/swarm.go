// Package cli — flowstate swarm subcommand.
//
// `flowstate swarm list` and `flowstate swarm validate` operate on the
// swarm manifests under the resolved swarm directory (the user's
// `~/.config/flowstate/swarms/` by default, overridable per-invocation
// with the local --swarm-dir flag for tests and ad-hoc runs). Both
// subcommands are pure read-side tooling — they do NOT exercise the
// engine, providers, or any in-flight integration surface — so they
// stay safe to land ahead of the wider swarm-runtime wire-up.
//
// `flowstate swarm run @<id>` is intentionally stubbed: the runner +
// engine wiring lands behind a separate feature branch. The stub exits
// 1 with a message naming the path forward so users discover the gap
// cleanly rather than hitting a panic or a silent no-op.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/spf13/cobra"
)

// swarmFlagDir is the local --swarm-dir flag name. Kept package-private
// because it is only meaningful inside this command tree; the root
// command does NOT promote it to a persistent flag (doing so would
// trigger initApp's app.New() reinit, which the cli-test isolation
// contract forbids).
const swarmFlagDir = "swarm-dir"

// newSwarmCmd creates the parent "swarm" command group with `list`,
// `validate`, and the `run` stub attached.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//     It is reserved for future engine wiring; today's subcommands only
//     read manifests off disk.
//
// Returns:
//   - A configured cobra.Command group with subcommands attached.
//
// Side effects:
//   - Registers list / validate / run subcommands on the returned
//     command.
func newSwarmCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "swarm",
		Short: "Inspect and validate swarm manifests",
		Long: "Operate on the swarm manifests under " +
			"~/.config/flowstate/swarms/: list registered swarms, " +
			"validate manifest schema, and (eventually) run them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().String(swarmFlagDir, "",
		"Override the swarm directory (defaults to ~/.config/flowstate/swarms)")
	cmd.AddCommand(
		newSwarmListCmd(getApp),
		newSwarmValidateCmd(getApp),
		newSwarmRunCmd(),
	)
	return cmd
}

// newSwarmListCmd builds the `flowstate swarm list` subcommand.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//     Reserved for future wiring; the listing today is filesystem-only.
//
// Returns:
//   - A configured cobra.Command with the local --swarm-dir flag.
//
// Side effects:
//   - Registers the --swarm-dir flag on the returned command.
func newSwarmListCmd(getApp func() *app.App) *cobra.Command {
	_ = getApp
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every registered swarm",
		Long: "Print every swarm manifest discovered under the resolved " +
			"swarm directory in tabular form (id, lead, members, gates).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := resolveSwarmDirFromFlags(cmd)
			return runSwarmList(cmd.OutOrStdout(), dir)
		},
	}
	return cmd
}

// newSwarmValidateCmd builds the `flowstate swarm validate [<id>]`
// subcommand. Optional positional id narrows validation to a single
// manifest; omit it to validate every discovered manifest.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command with the local --swarm-dir flag.
//
// Side effects:
//   - Registers the --swarm-dir flag on the returned command.
func newSwarmValidateCmd(getApp func() *app.App) *cobra.Command {
	_ = getApp
	cmd := &cobra.Command{
		Use:   "validate [<id>]",
		Short: "Validate one or all swarm manifests",
		Long: "Validate every swarm manifest under the resolved swarm " +
			"directory (or just the named id) and print pass/fail per " +
			"manifest. Exits non-zero on any validation failure.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := resolveSwarmDirFromFlags(cmd)
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return runSwarmValidate(cmd.OutOrStdout(), dir, id)
		},
	}
	return cmd
}

// newSwarmRunCmd builds the `flowstate swarm run @<id>` stub. It exists
// so users discover the missing engine wiring through a friendly error
// rather than the absence of a subcommand.
//
// Returns:
//   - A configured cobra.Command that always returns a deferred-feature
//     error.
//
// Side effects:
//   - None.
func newSwarmRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "run @<id>",
		Short:         "Run a swarm (coming soon)",
		Long:          "Execute a registered swarm by id. Engine wiring has not landed yet; this stub fails loudly so the gap is visible.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf(
				"swarm run is not yet implemented — coming soon, " +
					"wire up via engine integration")
		},
	}
}

// resolveSwarmDirFromFlags returns the swarm directory the subcommand
// should read from. Precedence: explicit --swarm-dir flag > the user's
// XDG-resolved ~/.config/flowstate/swarms path. Mirrors
// internal/app.resolveSwarmDir's choice of XDG_CONFIG_HOME so the CLI
// and the runtime stay aligned.
//
// Expected:
//   - cmd is a non-nil cobra.Command with the swarmFlagDir flag
//     registered.
//
// Returns:
//   - The absolute path the subcommand should read from.
//
// Side effects:
//   - None.
func resolveSwarmDirFromFlags(cmd *cobra.Command) string {
	if cmd != nil {
		if v, err := cmd.Flags().GetString(swarmFlagDir); err == nil && v != "" {
			return v
		}
	}
	return filepath.Join(config.Dir(), "swarms")
}

// loadSwarmManifests reads every *.yml / *.yaml manifest in dir and
// returns them sorted by id. A missing directory is treated as "no
// manifests" rather than an error so list/validate stay clean for
// fresh installs that have not seeded the directory yet.
//
// Expected:
//   - dir is a filesystem path (may be missing).
//
// Returns:
//   - The slice of loaded manifests sorted by id (may be nil).
//   - An error when the directory exists but contains malformed
//     manifests, or when LoadDir surfaces a per-file failure.
//
// Side effects:
//   - Reads dir from the filesystem.
func loadSwarmManifests(dir string) ([]*swarm.Manifest, error) {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat swarm directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("swarm directory %q is not a directory", dir)
	}

	manifests, err := swarm.LoadDir(dir)
	if err != nil {
		return manifests, err
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })
	return manifests, nil
}

// runSwarmList drives `flowstate swarm list`.
//
// Expected:
//   - w is a non-nil writer (cmd.OutOrStdout()).
//   - dir is the resolved swarm directory.
//
// Returns:
//   - nil on success (including the empty-directory case).
//   - A non-nil error when the directory cannot be loaded.
//
// Side effects:
//   - Reads dir from the filesystem.
//   - Writes a tabular report to w.
func runSwarmList(w io.Writer, dir string) error {
	manifests, err := loadSwarmManifests(dir)
	if err != nil && len(manifests) == 0 {
		return err
	}

	if len(manifests) == 0 {
		_, perr := fmt.Fprintf(w, "no swarms registered (looked in %s)\n", dir)
		return perr
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, perr := fmt.Fprintln(tw, "ID\tLEAD\tMEMBERS\tGATES"); perr != nil {
		return perr
	}
	for _, m := range manifests {
		if _, perr := fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n",
			m.ID, m.Lead, len(m.Members), len(m.Harness.Gates)); perr != nil {
			return perr
		}
	}
	if perr := tw.Flush(); perr != nil {
		return perr
	}

	if err != nil {
		_, perr := fmt.Fprintf(w, "\nnote: %v\n", err)
		return perr
	}
	return nil
}

// runSwarmValidate drives `flowstate swarm validate [<id>]`. Returns a
// non-nil error when any manifest fails validation so the cobra
// machinery surfaces a non-zero exit code; the per-manifest
// pass/fail line is still emitted for both branches so users can see
// which manifest failed.
//
// Expected:
//   - w is a non-nil writer.
//   - dir is the resolved swarm directory.
//   - id is the optional manifest id to narrow on (empty = all).
//
// Returns:
//   - nil when every checked manifest validates cleanly.
//   - A non-nil error naming the offending manifest path otherwise.
//
// Side effects:
//   - Reads dir from the filesystem.
//   - Writes per-manifest pass/fail lines to w.
func runSwarmValidate(w io.Writer, dir, id string) error {
	manifests, loadErr := loadSwarmManifests(dir)

	if id != "" {
		return validateSingle(w, manifests, dir, id, loadErr)
	}

	if loadErr != nil && len(manifests) == 0 {
		return loadErr
	}

	if len(manifests) == 0 {
		_, perr := fmt.Fprintf(w, "no swarms registered (looked in %s)\n", dir)
		return perr
	}

	for _, m := range manifests {
		if _, perr := fmt.Fprintf(w, "PASS\t%s\n", m.ID); perr != nil {
			return perr
		}
	}

	if loadErr != nil {
		return loadErr
	}
	return nil
}

// validateSingle handles the `validate <id>` path. Selecting one
// manifest from the loaded set keeps validation flowing even when
// other manifests in the directory are broken — the user asked about
// id, not the whole set.
//
// Expected:
//   - w is a non-nil writer.
//   - manifests is the (possibly partial) load result.
//   - dir is the resolved swarm directory used for error context.
//   - id is the manifest id to look up.
//   - loadErr is the LoadDir aggregate error (may be nil).
//
// Returns:
//   - nil when the manifest is found and validates.
//   - A non-nil error otherwise.
//
// Side effects:
//   - Writes PASS/FAIL lines to w.
func validateSingle(w io.Writer, manifests []*swarm.Manifest, dir, id string, loadErr error) error {
	for _, m := range manifests {
		if m.ID == id {
			if err := m.Validate(nil); err != nil {
				_, _ = fmt.Fprintf(w, "FAIL\t%s\n", id)
				return fmt.Errorf("validating swarm %q: %w", id, err)
			}
			_, perr := fmt.Fprintf(w, "PASS\t%s\n", id)
			return perr
		}
	}

	if loadErr != nil {
		return fmt.Errorf("swarm %q not found in %s: %w", id, dir, loadErr)
	}
	return fmt.Errorf("swarm %q not found in %s", id, dir)
}
