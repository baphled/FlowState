package cli

// PR5 C10 — `flowstate auth csrf-key gen` subcommand spec.
//
// Pins (all under --print-only, since the Auth Track follow-up flipped
// the default invocation to ALSO persist into config.yaml — the
// save-to-config behaviour is pinned in the external Ginkgo spec at
// auth_csrf_key_save_external_test.go; these internal tests stay
// focused on the print path so they have no filesystem side effects):
//   - `gen --print-only` prints a 32-byte (RawURLEncoding = 43 chars,
//     no padding) key to stdout followed by a single newline.
//   - Successive invocations produce different keys (CSPRNG, not
//     deterministic).
//   - Key length is exactly csrfKeyLen (32 bytes decoded), matching
//     gorilla/csrf's AuthKey expectation.
//   - Errors from the RNG bubble up as wrapped errors with "generating
//     csrf key:" prefix.
//   - Parent `csrf-key` (no args) prints help (no panic, exit 0).
//
// Standard Go test rather than Ginkgo because the existing cli/auth_*
// specs use plain testing.T (auth_anthropic_internal_test.go pattern);
// matches feedback_extend_existing_specs — extend the conventions of
// the neighbour code rather than introducing a new framework.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestAuthCSRFKeyGen_PrintsBase64URLKey(t *testing.T) {
	cmd := newAuthCSRFKeyCmd()
	cmd.SetArgs([]string{"gen", "--print-only"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("gen returned error: %v", err)
	}

	got := strings.TrimRight(out.String(), "\n")
	if got == "" {
		t.Fatalf("gen wrote nothing to stdout")
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("gen output missing trailing newline; got %q", out.String())
	}

	decoded, err := base64.RawURLEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("gen output is not RawURLEncoding base64: %v (got %q)", err, got)
	}
	if len(decoded) != csrfKeyLen {
		t.Errorf("decoded key length %d, want %d", len(decoded), csrfKeyLen)
	}
}

func TestAuthCSRFKeyGen_ProducesDistinctKeys(t *testing.T) {
	// CSPRNG smoke — two back-to-back invocations should produce
	// different keys. Probability of collision on 32 bytes is ~2^-256,
	// so a deterministic difference is a real expectation.
	keys := make([]string, 2)
	for i := range keys {
		cmd := newAuthCSRFKeyCmd()
		cmd.SetArgs([]string{"gen", "--print-only"})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("invocation %d returned error: %v", i, err)
		}
		keys[i] = strings.TrimRight(out.String(), "\n")
	}
	if keys[0] == keys[1] {
		t.Errorf("expected distinct keys across invocations; got both = %q", keys[0])
	}
}

func TestAuthCSRFKeyGen_WrapsRNGError(t *testing.T) {
	// Swap the generator for a deterministic failure; pin the wrapping
	// behaviour so the operator sees a clear error path rather than
	// some opaque crypto/rand message.
	orig := generateCSRFKey
	defer func() { generateCSRFKey = orig }()
	generateCSRFKey = func(_ []byte) error {
		return errors.New("simulated rng failure")
	}

	cmd := newAuthCSRFKeyCmd()
	cmd.SetArgs([]string{"gen", "--print-only"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when RNG fails, got nil")
	}
	if !strings.Contains(err.Error(), "generating csrf key") {
		t.Errorf("expected wrapped error prefix 'generating csrf key', got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "simulated rng failure") {
		t.Errorf("expected unwrapped cause in error chain, got %q", err.Error())
	}
}

func TestAuthCSRFKeyParent_NoArgsPrintsHelp(t *testing.T) {
	cmd := newAuthCSRFKeyCmd()
	cmd.SetArgs([]string{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("parent no-args returned error: %v", err)
	}
	helpStr := out.String()
	if !strings.Contains(helpStr, "csrf-key") {
		t.Errorf("expected help text to mention csrf-key; got %q", helpStr)
	}
	if !strings.Contains(helpStr, "gen") {
		t.Errorf("expected help text to list gen subcommand; got %q", helpStr)
	}
}
