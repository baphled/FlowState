package cli

// PR5 C10 — `flowstate auth csrf-key gen` subcommand. Prints a freshly
// generated 32-byte CSRF key (base64-URL encoded, no padding) that
// operators can paste into config.yaml's `auth.csrf_key` field or
// export via the FLOWSTATE_AUTH_CSRF_KEY env var.
//
// Why this exists: PR5/C10 removes the ephemeral-random fallback in
// installAuthFromConfig. When auth.enabled=true and no CSRF key is
// configured (config OR env), the server refuses to start with a clear
// error message pointing at this command. Operators run the generator
// once at deploy time and stash the output in their config / secrets
// manager.

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/spf13/cobra"
)

// csrfKeyLen is the byte length of the generated key. 32 bytes (256
// bits) matches gorilla/csrf's expected AuthKey size and the
// session-token mint size (auth/session.go:317), so the same primitive
// underlies both surfaces.
const csrfKeyLen = 32

// generateCSRFKey is the indirection point for tests — allows the spec
// to swap in a deterministic source without monkey-patching crypto/rand.
// Production calls cryptoRandRead.
var generateCSRFKey = cryptoRandRead

// cryptoRandRead is the production implementation: 32 random bytes from
// crypto/rand. Pulled out so generateCSRFKey can be overridden in
// tests.
func cryptoRandRead(buf []byte) error {
	_, err := rand.Read(buf)
	return err
}

// newAuthCSRFKeyCmd returns the `flowstate auth csrf-key` parent and
// its `gen` subcommand. The parent is a no-op (prints help) so future
// subcommands (verify, rotate) can land here without breaking the CLI
// shape.
//
// Expected:
//   - Caller wires the result through newAuthCmd's AddCommand.
//
// Returns:
//   - A configured *cobra.Command for `flowstate auth csrf-key`.
//
// Side effects:
//   - On `gen`: writes the encoded key to cmd.OutOrStdout followed by a
//     trailing newline.
func newAuthCSRFKeyCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "csrf-key",
		Short: "Generate and manage the FlowState API CSRF signing key",
		Long: `Manage the CSRF signing key used by the FlowState API auth track.

The CSRF key is a 32-byte HMAC secret consumed by gorilla/csrf to sign
the _csrf cookie. It MUST be stable across server restarts — a fresh
key invalidates every active session, forcing users to re-authenticate.

Resolution order (highest precedence first):
  1. FLOWSTATE_AUTH_CSRF_KEY environment variable
  2. auth.csrf_key in config.yaml
  3. (PR5/C10) FAIL — the server refuses to start with auth.enabled=true
     and no key configured. Run "flowstate auth csrf-key gen" to mint
     one, then store it in config or the env.

The previous ephemeral-random fallback (PR4/C9) is removed in PR5/C10 —
operators must configure the key explicitly. Dev / smoke runs that don't
want auth can set auth.enabled=false.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(newAuthCSRFKeyGenCmd())
	return parent
}

// newAuthCSRFKeyGenCmd returns the `gen` leaf subcommand. Mints a
// 32-byte random key, encodes as URL-safe base64 (RawURLEncoding — no
// padding, matches auth/session.go's mintToken idiom), and prints to
// stdout.
//
// Returns:
//   - A configured *cobra.Command.
//
// Side effects:
//   - Writes the encoded key + newline to cmd.OutOrStdout.
//   - Returns an error if crypto/rand.Read fails (effectively never on
//     Linux but pinned defensively).
func newAuthCSRFKeyGenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gen",
		Short: "Print a freshly-generated CSRF signing key",
		Long: `Generate and print a 32-byte random CSRF signing key, base64-URL encoded.

Usage:
  # Pipe straight into the env for one-shot dev:
  export FLOWSTATE_AUTH_CSRF_KEY=$(flowstate auth csrf-key gen)

  # Or paste into config.yaml:
  flowstate auth csrf-key gen
  # → ABC123def456...
  # Then in ~/.config/flowstate/config.yaml:
  #   auth:
  #     csrf_key: ABC123def456...`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			buf := make([]byte, csrfKeyLen)
			if err := generateCSRFKey(buf); err != nil {
				return fmt.Errorf("generating csrf key: %w", err)
			}
			encoded := base64.RawURLEncoding.EncodeToString(buf)
			fmt.Fprintln(cmd.OutOrStdout(), encoded)
			return nil
		},
	}
}
