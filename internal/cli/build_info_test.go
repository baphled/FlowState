package cli

// Startup-banner spec. Lives in `package cli` (internal) so the test
// can reach the unexported extractBuildIdentity / buildStartupBanner
// helpers without exposing them via export_test.go.
//
// Motivation: an operator-visible startup line that pins the running
// binary's identity (VCS revision + build time + dirty flag) and lists
// which fail-closed gates are compiled in. The May 2026 audit found a
// session where a pre-PR7 binary kept serving requests after PR7's
// source had landed — the running session offered no signal that the
// gate was inactive. The banner closes that gap.
//
// The banner builder is split into two pure functions so neither
// process startup nor a real *http.Server is required to drive the
// behaviour:
//
//   - extractBuildIdentity folds three sources into one struct: the
//     -ldflags -X package vars (cmd/flowstate/main.go's version /
//     commit / date), runtime/debug.ReadBuildInfo() VCS settings, and
//     a baked-in gates list. ldflags wins per field; ReadBuildInfo
//     fills the rest; gates is a code-derived constant (no flag
//     plumbing per brief).
//   - buildStartupBanner renders the struct into the single
//     human-readable line written to stdout at serve startup.
//
// Pins:
//   - ldflags vars (when non-default) override VCS revision + build
//     time. This honours the existing version/commit/date precedence
//     in cli.SetVersion.
//   - When ldflags are at their defaults ("dev" / "unknown") and VCS
//     info is available, the banner shows the VCS revision (short
//     SHA — first 7 chars) and the VCS commit time.
//   - When neither ldflags nor VCS info is available, the banner
//     still emits with sha=unknown / built=unknown / dirty=unknown.
//     This is the verification-honest path: an operator probing a
//     binary built without -buildvcs sees the gap clearly rather
//     than a confidently-wrong identity.
//   - dirty flag (vcs.modified) carries through.
//   - go=<runtime version> is always present (runtime.Version()).
//   - gates list is the compiled-in fail-closed gates.
//     Currently:
//       - capabilities.tools (engine.go:4459-4476, PR7 4b25f026)
//       - delegation_allowlist (the static-vs-swarm gate at
//         engine.go:2067-2123, post-17b1731a swarm authority)
//     The list is a package-level constant; adding a gate requires
//     touching this code, which is the audit-friendly outcome.
//   - The rendered banner format is the single-line key=value style
//     the brief specifies, matching the existing `Starting server
//     on %s` stdout line in serve.go.

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestExtractBuildIdentity_PrefersLdflagsVarsOverVCS(t *testing.T) {
	// ldflags vars are non-default ("dev" / "unknown" / "unknown" are
	// the defaults in cmd/flowstate/main.go). When the operator builds
	// with `-ldflags "-X main.commit=abc1234"` the banner must show the
	// ldflags value, not the VCS revision baked into the binary's
	// debug.BuildInfo (which may be stale relative to a packaging-time
	// override).
	info := &debug.BuildInfo{
		GoVersion: "go1.22.5",
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "deadbeefdeadbeefdeadbeefdeadbeef"},
			{Key: "vcs.time", Value: "2026-01-01T00:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	got := extractBuildIdentity(info, "1.2.3", "abc1234", "2026-05-21T04:32:31Z")
	if got.Revision != "abc1234" {
		t.Errorf("ldflags commit must override VCS revision; got Revision=%q want %q",
			got.Revision, "abc1234")
	}
	if got.BuildTime != "2026-05-21T04:32:31Z" {
		t.Errorf("ldflags date must override VCS time; got BuildTime=%q want %q",
			got.BuildTime, "2026-05-21T04:32:31Z")
	}
}

func TestExtractBuildIdentity_FallsBackToVCSWhenLdflagsAreDefaults(t *testing.T) {
	// `cmd/flowstate/main.go` ships `version = "dev"`, `commit = "unknown"`,
	// `date = "unknown"` and Makefile's `make build` does not currently
	// inject -ldflags. The banner must surface the VCS info in that case
	// so a `make build` artifact still pins to a SHA. Short-form is the
	// first 7 chars (matches git's conventional short SHA).
	info := &debug.BuildInfo{
		GoVersion: "go1.22.5",
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "21916636aabbccdd11223344556677889900aabb"},
			{Key: "vcs.time", Value: "2026-05-20T12:00:00Z"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	got := extractBuildIdentity(info, "dev", "unknown", "unknown")
	// Git's conventional short-SHA is 7 chars (matches `git log --oneline`
	// and the brief's `sha=abc1234` example).
	if got.Revision != "2191663" {
		t.Errorf("default ldflags must fall through to VCS short-SHA (7 chars); got Revision=%q want %q",
			got.Revision, "2191663")
	}
	if got.BuildTime != "2026-05-20T12:00:00Z" {
		t.Errorf("default ldflags must fall through to VCS time; got BuildTime=%q want %q",
			got.BuildTime, "2026-05-20T12:00:00Z")
	}
	if got.Dirty {
		t.Errorf("vcs.modified=false must yield Dirty=false; got Dirty=true")
	}
}

func TestExtractBuildIdentity_DirtyFlagFlowsThrough(t *testing.T) {
	// A binary built from an uncommitted working tree must surface
	// dirty=true so the operator can correlate audit findings with
	// "this isn't a clean release artifact".
	info := &debug.BuildInfo{
		GoVersion: "go1.22.5",
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "21916636aabbccdd11223344556677889900aabb"},
			{Key: "vcs.time", Value: "2026-05-20T12:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	got := extractBuildIdentity(info, "dev", "unknown", "unknown")
	if !got.Dirty {
		t.Errorf("vcs.modified=true must yield Dirty=true; got Dirty=false")
	}
}

func TestExtractBuildIdentity_UnknownWhenNeitherSourcePresent(t *testing.T) {
	// Probe-built binaries (and `go run`) ship without VCS info. The
	// banner must still emit a line — silently dropping it would put
	// the operator in the same epistemic state the audit found
	// (running session, no signal). Honest "unknown" is the contract.
	info := &debug.BuildInfo{
		GoVersion: "go1.22.5",
		Settings:  nil,
	}
	got := extractBuildIdentity(info, "dev", "unknown", "unknown")
	if got.Revision != "unknown" {
		t.Errorf("no VCS, default ldflags: Revision must be %q; got %q",
			"unknown", got.Revision)
	}
	if got.BuildTime != "unknown" {
		t.Errorf("no VCS, default ldflags: BuildTime must be %q; got %q",
			"unknown", got.BuildTime)
	}
	if got.Dirty {
		t.Errorf("no VCS, default ldflags: Dirty must be false; got Dirty=true")
	}
}

func TestExtractBuildIdentity_GoVersionFromBuildInfo(t *testing.T) {
	info := &debug.BuildInfo{
		GoVersion: "go1.22.5",
		Settings:  nil,
	}
	got := extractBuildIdentity(info, "dev", "unknown", "unknown")
	if got.GoVersion != "go1.22.5" {
		t.Errorf("GoVersion must come from BuildInfo; got %q want %q",
			got.GoVersion, "go1.22.5")
	}
}

func TestExtractBuildIdentity_GoVersionFallsBackToRuntimeWhenBuildInfoEmpty(t *testing.T) {
	// debug.ReadBuildInfo() returns ok=false in tightly stripped
	// builds. The wiring at the call site passes a non-nil BuildInfo
	// even in that case (zero-valued), and the extractor must then
	// fall through to runtime.Version() so the field is never empty.
	got := extractBuildIdentity(&debug.BuildInfo{}, "dev", "unknown", "unknown")
	if got.GoVersion != runtime.Version() {
		t.Errorf("empty BuildInfo.GoVersion must fall back to runtime.Version(); got %q want %q",
			got.GoVersion, runtime.Version())
	}
}

func TestExtractBuildIdentity_GatesListAlwaysIncludesCapabilitiesAndDelegation(t *testing.T) {
	// The gates list is the load-bearing field of the banner — it tells
	// the operator which fail-closed enforcement is compiled into THIS
	// binary. The capabilities.tools gate at engine.go:4459-4476 (PR7
	// 4b25f026) and the delegation_allowlist gate are both shipped on
	// agent-platform; the banner must surface them.
	info := &debug.BuildInfo{
		GoVersion: "go1.22.5",
		Settings:  nil,
	}
	got := extractBuildIdentity(info, "dev", "unknown", "unknown")
	gates := strings.Join(got.Gates, ",")
	if !strings.Contains(gates, "capabilities.tools") {
		t.Errorf("gates list must include capabilities.tools; got %q", gates)
	}
	if !strings.Contains(gates, "delegation_allowlist") {
		t.Errorf("gates list must include delegation_allowlist; got %q", gates)
	}
}

func TestBuildStartupBanner_RendersAllFieldsInKeyValueLine(t *testing.T) {
	// The banner format is the single-line shape the brief specifies.
	// Pinning each key here is intentional: the audit needs to grep
	// these tokens out of historical logs, so the field labels are a
	// stable contract.
	id := BuildIdentity{
		Revision:  "abc1234",
		BuildTime: "2026-05-21T04:32:31Z",
		Dirty:     false,
		GoVersion: "go1.22.5",
		Gates:     []string{"capabilities.tools", "delegation_allowlist"},
	}
	got := buildStartupBanner(id)
	for _, want := range []string{
		"FlowState starting",
		"sha=abc1234",
		"built=2026-05-21T04:32:31Z",
		"dirty=false",
		"go=go1.22.5",
		"gates=capabilities.tools,delegation_allowlist",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q; full line: %q", want, got)
		}
	}
}

func TestBuildStartupBanner_DirtyTrueRendersExplicitly(t *testing.T) {
	// dirty=true is the high-signal case for the operator — must NOT
	// be rendered as the absence of the field.
	id := BuildIdentity{
		Revision:  "abc1234",
		BuildTime: "2026-05-21T04:32:31Z",
		Dirty:     true,
		GoVersion: "go1.22.5",
		Gates:     []string{"capabilities.tools"},
	}
	got := buildStartupBanner(id)
	if !strings.Contains(got, "dirty=true") {
		t.Errorf("dirty=true must render explicitly; got %q", got)
	}
}

func TestBuildStartupBanner_UnknownIdentityStillEmitsLine(t *testing.T) {
	// Honest-unknown: the banner must always emit so the operator
	// gets a startup signal even when build identity is opaque. A
	// silent omission is the failure mode this whole banner is meant
	// to prevent.
	id := BuildIdentity{
		Revision:  "unknown",
		BuildTime: "unknown",
		Dirty:     false,
		GoVersion: runtime.Version(),
		Gates:     []string{"capabilities.tools", "delegation_allowlist"},
	}
	got := buildStartupBanner(id)
	if !strings.HasPrefix(got, "FlowState starting") {
		t.Errorf("banner must start with the canonical prefix; got %q", got)
	}
	if !strings.Contains(got, "sha=unknown") {
		t.Errorf("unknown SHA must render as sha=unknown so the gap is visible; got %q", got)
	}
}
