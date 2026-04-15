// Package agent_test — H3 audit coverage for per-agent
// CompactionThreshold range validation.
//
// The manifest field has always been declared but never read; H3
// wires it into engine.autoCompactionThreshold as a per-agent
// override. For that override to be safe the loader must range-
// validate the field on load — same (0, 1] rule the global
// auto-compaction threshold is held to.
package agent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
)

// TestLoadManifestJSON_RejectsNegativeCompactionThreshold pins the
// negative side. A manifest carrying a CompactionThreshold below zero
// is a misconfiguration — ratios are inherently non-negative — and
// the loader must refuse to return a Manifest constructed from that
// input. Surfacing at load time means the operator sees a loud
// error with the file path, not a silent slide through runtime.
func TestLoadManifestJSON_RejectsNegativeCompactionThreshold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	body := map[string]any{
		"id":   "agent-negative",
		"name": "Agent with negative threshold",
		"context_management": map[string]any{
			"compaction_threshold": -0.1,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = agent.LoadManifestJSON(path)
	if err == nil {
		t.Fatalf("LoadManifestJSON(-0.1) = nil; want error")
	}
	if !strings.Contains(err.Error(), "compaction_threshold") {
		t.Fatalf("LoadManifestJSON error = %q; want contains 'compaction_threshold'", err.Error())
	}
}

// TestLoadManifestJSON_RejectsAboveOneCompactionThreshold pins the
// other end of the range. A value > 1 means "the compactor fires
// only when recent tokens exceed the whole budget", which is
// nonsensical because the budget IS the token budget — there is no
// load above it to measure.
func TestLoadManifestJSON_RejectsAboveOneCompactionThreshold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	body := map[string]any{
		"id":   "agent-above-one",
		"name": "Agent with above-one threshold",
		"context_management": map[string]any{
			"compaction_threshold": 1.5,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = agent.LoadManifestJSON(path)
	if err == nil {
		t.Fatalf("LoadManifestJSON(1.5) = nil; want error")
	}
	if !strings.Contains(err.Error(), "compaction_threshold") {
		t.Fatalf("LoadManifestJSON error = %q; want contains 'compaction_threshold'", err.Error())
	}
}

// TestLoadManifestJSON_AcceptsZeroCompactionThreshold pins the zero
// case. Zero means "inherit global" — the loader must preserve the
// caller's choice to opt out of a per-agent override. The default-
// applier currently fills 0 with 0.75, but the semantic under H3 is
// "zero is legal as input"; the applier's behaviour is orthogonal
// and tested separately.
func TestLoadManifestJSON_AcceptsZeroCompactionThreshold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ok.json")

	body := map[string]any{
		"id":   "agent-zero",
		"name": "Agent with zero threshold",
		"context_management": map[string]any{
			"compaction_threshold": 0.0,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := agent.LoadManifestJSON(path); err != nil {
		t.Fatalf("LoadManifestJSON(0.0) = %v; want nil", err)
	}
}

// TestLoadManifestJSON_CompactionThresholdErrorMessageGuidance pins
// the richer operator-facing error wording. The (0, 1] range rule is
// the same one enforced by compression.auto_compaction.threshold, so
// the manifest error should mirror its actionable phrasing: operators
// need to know that values <= 0 never trigger and values > 1 trigger
// every turn, otherwise they have no way to diagnose their mistake
// from the error alone.
func TestLoadManifestJSON_CompactionThresholdErrorMessageGuidance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value any
	}{
		{name: "negative", value: -0.1},
		{name: "above one", value: 1.5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertRichCompactionThresholdError(t, tc.name, tc.value)
		})
	}
}

// assertRichCompactionThresholdError writes a manifest with the given
// out-of-range threshold, invokes the loader, and asserts the error
// carries both the field name and the operator guidance. Extracted so
// the table-driven parent stays under the cognitive-complexity cap.
func assertRichCompactionThresholdError(t *testing.T, name string, value any) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	body := map[string]any{
		"id":   "agent-" + name,
		"name": "Agent with bad threshold",
		"context_management": map[string]any{
			"compaction_threshold": value,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = agent.LoadManifestJSON(path)
	if err == nil {
		t.Fatalf("LoadManifestJSON(%v) = nil; want error", value)
	}
	msg := err.Error()
	// Each fragment is one piece of diagnostic information operators
	// rely on to fix a misconfigured manifest without reading source.
	wants := []string{
		"compaction_threshold", // field name (grep-able)
		"(0.0, 1.0]",           // sibling notation with global validator
		"never trigger",        // guidance for <=0
		"every turn",           // guidance for >1
	}
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q; want substring %q", msg, want)
		}
	}
}

// TestLoadManifestJSON_AcceptsBoundaryOneCompactionThreshold pins
// the inclusive upper bound. 1.0 is legal — it means "compact when
// recent load equals the full budget". It is conservative, but legal.
func TestLoadManifestJSON_AcceptsBoundaryOneCompactionThreshold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ok.json")

	body := map[string]any{
		"id":   "agent-one",
		"name": "Agent with threshold 1.0",
		"context_management": map[string]any{
			"compaction_threshold": 1.0,
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	m, err := agent.LoadManifestJSON(path)
	if err != nil {
		t.Fatalf("LoadManifestJSON(1.0) = %v; want nil", err)
	}
	if m.ContextManagement.CompactionThreshold != 1.0 {
		t.Fatalf("manifest.ContextManagement.CompactionThreshold = %v; want 1.0", m.ContextManagement.CompactionThreshold)
	}
}
