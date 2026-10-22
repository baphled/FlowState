package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// defaultStdinIsTerminal is the production TTY probe. Reads os.Stdin's
// fd and asks term.IsTerminal whether it's connected to a real terminal.
// Lifted to a separate name so export_test.go's RestoreStdinIsTerminal
// hook has something stable to point stdinIsTerminal back at.
//
// Expected:
//   - None.
//
// Returns:
//   - true when os.Stdin is a TTY; false otherwise (pipe, redirect,
//     /dev/null).
//
// Side effects:
//   - None (read-only syscall).
func defaultStdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// stdinIsTerminal reports whether os.Stdin is connected to a TTY. Exposed
// as a package-level var so the auth_reset_test specs can substitute a
// fake (the production process never re-assigns).
//
// Used by `flowstate auth reset` to enforce --force outside a TTY (plan
// §OD-H + §Test Strategy line 647).
var stdinIsTerminal = defaultStdinIsTerminal

// sessionStoreCleaner is the narrow slice of store.Store the reset path
// needs. The cobra command depends on the interface rather than the
// concrete store implementation so tests can pass an in-memory fake
// without standing up the real persistence layer.
//
// Expected:
//   - Cleanup(ctx, before) is called with a `before` far enough in the
//     future that every record is past expiry (reset wipes ALL sessions).
type sessionStoreCleaner interface {
	Cleanup(ctx context.Context, before time.Time) error
}

// resetStore is the package-level store the reset command operates on.
// Production callers set this from cmd/serve's boot wiring; tests
// substitute a fake. Nil store means "no store wired" and the reset
// command logs that fact without failing — `auth reset` should still
// remove users.json even when the session store isn't reachable, since
// the operator may invoke it in a disaster-recovery context (memory
// feedback_close_latent_surfaces_too — close BOTH layers).
var resetStore sessionStoreCleaner

// SetResetStore wires the session store the `flowstate auth reset`
// command sweeps. Called from cmd/serve and from tests. Idempotent —
// repeated calls replace the prior wiring.
//
// Expected:
//   - s may be nil (reset will skip the store-cleanup step + log).
//
// Returns:
//   - None.
//
// Side effects:
//   - Replaces resetStore in package state.
func SetResetStore(s sessionStoreCleaner) {
	resetStore = s
}

// newAuthResetCmd creates the `flowstate auth reset` subcommand
// (plan §OD-H + §"Bootstrap UX" → "Admin reset").
//
// Behaviour:
//   - Wipes users.json (moves to users.json.bak.<unix-ts> so the
//     operator can recover if needed).
//   - Sweeps the session store via Cleanup with a future `before`,
//     dropping every record atomically.
//   - --force is required outside a TTY (plan §Test Strategy line 647).
//     Inside a TTY, prompts for confirmation when --force is absent.
//
// Expected:
//   - None.
//
// Returns:
//   - A configured cobra.Command.
//
// Side effects:
//   - None at construction time.
func newAuthResetCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset session store and clear users.json (admin recovery)",
		Long: "Wipe the FlowState auth state: clear the session store and\n" +
			"move users.json to users.json.bak.<unix-ts>. Intended for\n" +
			"operator-level recovery; --force is required outside a TTY\n" +
			"(plan §OD-H). Plan reference: FlowState API Auth Track (May\n" +
			"2026) §\"Bootstrap UX\" → \"Admin reset\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthReset(cmd, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip TTY prompt; required when stdin is not a terminal")
	return cmd
}

// runAuthReset performs the reset.
//
// Expected:
//   - cmd is non-nil.
//   - When force is false AND stdin is not a TTY → refuse with a guard
//     error per plan §OD-H + §Test Strategy line 647.
//
// Returns:
//   - nil on success or on the no-op path (no users.json + no store).
//   - Error on guard violation, atomic-move failure, or store-cleanup
//     failure.
//
// Side effects:
//   - Moves users.json to users.json.bak.<ts> (atomic rename).
//   - Calls resetStore.Cleanup with a far-future `before` so every
//     record is past expiry.
//   - Prints a release-notes-style log to cmd.OutOrStdout and
//     mirrors via slog.Info.
func runAuthReset(cmd *cobra.Command, force bool) error {
	if !force && !stdinIsTerminal() {
		return errors.New(
			"refusing to run without --force outside a TTY (plan §OD-H)")
	}

	out := cmd.OutOrStdout()
	usersPath := usersJSONPath()

	// 1. Wipe users.json (move to backup for recovery).
	var backupPath string
	if _, err := os.Stat(usersPath); err == nil {
		backupPath = fmt.Sprintf("%s.bak.%d", usersPath, time.Now().Unix())
		if err := os.Rename(usersPath, backupPath); err != nil {
			return fmt.Errorf("moving users.json to backup: %w", err)
		}
		fmt.Fprintf(out, "Moved %s → %s\n", usersPath, backupPath)
		slog.Info("auth_reset_users_json_moved",
			"path", usersPath,
			"backup", backupPath,
		)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat users.json: %w", err)
	} else {
		fmt.Fprintf(out, "No users.json at %s (skipping)\n", usersPath)
	}

	// 2. Sweep the session store.
	if resetStore == nil {
		fmt.Fprintln(out, "No session store wired; skipping store cleanup "+
			"(boot the server with auth enabled to clear sessions)")
		slog.Info("auth_reset_no_store_wired")
	} else {
		// Use a `before` 24h in the future so EVERY record (including
		// freshly-minted ones) is past expiry from the sweep's POV.
		future := time.Now().Add(24 * time.Hour)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := resetStore.Cleanup(ctx, future); err != nil {
			return fmt.Errorf("clearing session store: %w", err)
		}
		fmt.Fprintln(out, "Cleared session store")
		slog.Info("auth_reset_store_cleared")
	}

	// 3. Note on signing-secret rotation: the v1 session signing key is
	// the scs/v2 token-generation primitive (32B CSPRNG per Begin), not
	// a long-lived signing secret. There is nothing to rotate in v1; v2
	// (per OD-H) regenerates the shared-secret modes' secret. Logged so
	// the operator sees the intent.
	fmt.Fprintln(out, "Session signing secret rotation: v1 uses per-record "+
		"CSPRNG tokens — no long-lived secret to rotate")
	slog.Info("auth_reset_signing_secret_noop_v1")
	return nil
}
