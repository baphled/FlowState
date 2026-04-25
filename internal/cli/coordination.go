// Package cli provides the coordination subcommand group.
//
// `flowstate coordination prune` identifies (and optionally deletes)
// orphaned keys in the coord-store. The store has no per-key lifecycle
// metadata, so the heuristic mirrors the documented agent contract:
// every legitimate write is `<chainID>/<keyname>` where chainID is
// either planner-allocated or a chain-<unixnano> fallback from
// newDelegationChainID. Anything else is stranded and unreachable by
// living chains.
//
// Safety invariants:
//   - Dry-run by default; --apply is required to delete.
//   - chain-<unixnano> keys are preserved unless --include-chain-format
//     opts in to destructive sweeping.
//   - --older-than gates the run on file mtime (the FileStore has no
//     per-key timestamps).
//   - Missing-file path is a clean no-op for fresh installs.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/spf13/cobra"
)

// chainIDPattern matches the canonical delegation chainID format
// produced by internal/engine/delegation.newDelegationChainID
// (`chain-<unixnano>`). Any future change to that constructor MUST
// update this pattern in lockstep — the unit spec
// "excludes legitimate chain-<nano> keys from the orphan set by
// default" is the canary.
var chainIDPattern = regexp.MustCompile(`^chain-\d+$`)

// orphanReason categorises why a key is considered stranded.
type orphanReason string

const (
	reasonNoChainPrefix    orphanReason = "no-chain-prefix"
	reasonFlowstateNS      orphanReason = "flowstate-namespace"
	reasonNonChainPrefix   orphanReason = "non-chain-prefix"
	reasonChainFormat      orphanReason = "chain-format"
)

// orphan describes a single stranded key in the coord-store.
type orphan struct {
	key    string
	size   int
	reason orphanReason
}

// pruneOptions holds the parsed flag values for one run.
type pruneOptions struct {
	apply              bool
	prefix             string
	olderThan          time.Duration
	includeChainFormat bool
}

// newCoordinationCmd creates the parent "coordination" command group.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command group with the prune subcommand attached.
//
// Side effects:
//   - Registers the prune subcommand.
func newCoordinationCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coordination",
		Short: "Manage the coordination store",
		Long:  "Operate on the cross-agent coordination store: prune orphan keys, etc.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCoordinationPruneCmd(getApp))
	return cmd
}

// newCoordinationPruneCmd creates the "coordination prune" subcommand.
//
// Expected:
//   - getApp is a non-nil function returning the application instance.
//
// Returns:
//   - A configured cobra.Command with --apply, --prefix,
//     --include-chain-format, and --older-than flags.
//
// Side effects:
//   - Reads the coord-store JSON file at <DataDir>/coordination.json.
//   - Issues coordination.Store.Delete on classified orphans only when
//     --apply is set.
func newCoordinationPruneCmd(getApp func() *app.App) *cobra.Command {
	var opts pruneOptions

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Identify and (opt-in) delete orphaned coord-store keys",
		Long: "Scan the coordination store for keys that no live delegation chain " +
			"can ever reach (top-level keys, ad-hoc prefixes, flowstate-namespace " +
			"writes) and report or delete them.\n\n" +
			"Dry-run by default — pass --apply to actually delete. Sweeping the " +
			"canonical chain-<unixnano> namespace requires --include-chain-format.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoordinationPrune(cmd, getApp(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.apply, "apply", false, "Actually delete (default is dry-run)")
	cmd.Flags().StringVar(&opts.prefix, "prefix", "", "Restrict to keys under this exact prefix")
	cmd.Flags().DurationVar(&opts.olderThan, "older-than", 0,
		"Only run if coord-store file mtime exceeds this age (0 = disabled)")
	cmd.Flags().BoolVar(&opts.includeChainFormat, "include-chain-format", false,
		"Also sweep chain-<unixnano> keys (destructive)")
	return cmd
}

// runCoordinationPrune drives one prune run end-to-end.
//
// Expected:
//   - cmd is a non-nil cobra.Command with an initialised output writer.
//   - application is a non-nil App with Config.DataDir resolved.
//
// Returns:
//   - nil on success (including the no-op missing-file path and the
//     gated-by-mtime path).
//   - non-nil error if the store cannot be opened or a Delete fails.
//
// Side effects:
//   - Reads <DataDir>/coordination.json.
//   - Deletes classified orphans through the coordination.Store API
//     when --apply is set.
//   - Writes a human-readable report to cmd.OutOrStdout().
func runCoordinationPrune(cmd *cobra.Command, application *app.App, opts pruneOptions) error {
	w := cmd.OutOrStdout()
	coordPath := filepath.Join(application.Config.DataDir, "coordination.json")

	info, err := os.Stat(coordPath)
	if errors.Is(err, os.ErrNotExist) {
		_, perr := fmt.Fprintf(w, "No coordination store at %s; 0 orphan(s) to prune\n", coordPath)
		return perr
	}
	if err != nil {
		return fmt.Errorf("stat coordination store: %w", err)
	}

	if opts.olderThan > 0 {
		age := time.Since(info.ModTime())
		if age < opts.olderThan {
			_, perr := fmt.Fprintf(w,
				"Coordination store at %s is too recent (age=%s, threshold=%s); run skipped\n",
				coordPath, age.Round(time.Second), opts.olderThan)
			return perr
		}
	}

	store, err := coordination.NewFileStore(coordPath)
	if err != nil {
		return fmt.Errorf("open coordination store: %w", err)
	}

	keys, err := store.List(opts.prefix)
	if err != nil {
		return fmt.Errorf("list coord-store keys: %w", err)
	}

	orphans := classifyOrphans(store, keys, opts.includeChainFormat)
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].key < orphans[j].key })

	header := fmt.Sprintf("Scanning coordination store at %s", coordPath)
	if opts.apply {
		header += " (apply: deleting classified orphans)"
	} else {
		header += " (dry-run: pass --apply to delete)"
	}
	if opts.prefix != "" {
		header += fmt.Sprintf(" [prefix=%q]", opts.prefix)
	}
	if _, perr := fmt.Fprintln(w, header); perr != nil {
		return perr
	}

	var totalBytes int
	for _, o := range orphans {
		if _, perr := fmt.Fprintf(w, "  %-60s %6d B  %s\n", o.key, o.size, o.reason); perr != nil {
			return perr
		}
		totalBytes += o.size
	}

	if opts.apply {
		for _, o := range orphans {
			if delErr := store.Delete(o.key); delErr != nil {
				return fmt.Errorf("deleting %q: %w", o.key, delErr)
			}
		}
	}

	suffix := "(dry-run: no keys deleted)"
	if opts.apply {
		suffix = "(applied: keys deleted)"
	}
	_, perr := fmt.Fprintf(w, "Summary: %d orphan(s) totalling %d B %s\n",
		len(orphans), totalBytes, suffix)
	return perr
}

// classifyOrphans walks the listed keys, classifies each, and returns
// only the keys deemed orphans for this run. Each orphan is sized via
// store.Get so the report can show a per-key byte count.
//
// Expected:
//   - store is a non-nil coordination.Store.
//   - keys is the list of keys to consider (already prefix-narrowed by
//     the caller).
//   - includeChainFormat toggles whether chain-<unixnano> keys are
//     considered orphans.
//
// Returns:
//   - The slice of orphans (may be empty).
//
// Side effects:
//   - Reads each candidate key via store.Get to measure its byte size.
//     Read errors are skipped silently — the key is still listed but
//     with size 0.
func classifyOrphans(store coordination.Store, keys []string, includeChainFormat bool) []orphan {
	orphans := make([]orphan, 0, len(keys))
	for _, k := range keys {
		reason, ok := classifyKey(k, includeChainFormat)
		if !ok {
			continue
		}
		size := 0
		if val, err := store.Get(k); err == nil {
			size = len(val)
		}
		orphans = append(orphans, orphan{key: k, size: size, reason: reason})
	}
	return orphans
}

// classifyKey applies the orphan heuristic to a single key.
//
// Expected:
//   - key is a coord-store key.
//   - includeChainFormat toggles the chain-<unixnano> reachability
//     contract from "preserved" to "swept".
//
// Returns:
//   - The orphan reason and true when the key is considered stranded.
//   - "" and false when the key is legitimate.
//
// Side effects:
//   - None.
func classifyKey(key string, includeChainFormat bool) (orphanReason, bool) {
	if strings.HasPrefix(key, "flowstate/") {
		return reasonFlowstateNS, true
	}
	idx := strings.Index(key, "/")
	if idx < 0 {
		return reasonNoChainPrefix, true
	}
	prefix := key[:idx]
	if chainIDPattern.MatchString(prefix) {
		if includeChainFormat {
			return reasonChainFormat, true
		}
		return "", false
	}
	return reasonNonChainPrefix, true
}

// Compile-time assertion that the io package import stays exercised
// by an exported helper would normally live here; classifyOrphans
// already covers io use indirectly through store.Get's []byte return.
var _ io.Writer = (*os.File)(nil)
