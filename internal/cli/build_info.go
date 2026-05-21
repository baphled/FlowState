package cli

// Startup banner — operator-visible identity line for the running
// `flowstate serve` binary. Purpose: pin which SHA + build time +
// dirty flag + compiled-in fail-closed gates the binary carries, so
// audits can distinguish "fix landed in source but binary is stale"
// from "fix is live". The May 2026 audit found a session where a
// pre-PR7 binary kept serving requests after PR7's source had landed;
// nothing in the running process signalled the gate was inactive. The
// banner closes that gap by emitting an unambiguous line to stdout at
// startup, BEFORE the existing "Starting server on %s" line.
//
// The banner builder is split into two pure functions so it is
// testable without a real *http.Server or process restart:
//
//   - extractBuildIdentity composes -ldflags package vars
//     (cmd/flowstate/main.go's version / commit / date), the embedded
//     debug.BuildInfo VCS settings, and the compiled-in gates list
//     into a BuildIdentity struct.
//   - buildStartupBanner renders the struct as the single-line stdout
//     log message.
//
// Precedence: ldflags > VCS info > "unknown". The brief mandates "use
// whichever path is already present; don't introduce a new
// build-flag scheme" — main.go's version/commit/date package vars
// already exist for cli.SetVersion; we read the same trio here so a
// single ldflags wiring serves both.

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// compiledInGates lists the fail-closed enforcement gates baked into
// THIS binary. Adding a gate requires touching this list — the
// audit-friendly outcome: a missing gate name in the startup banner
// means the source-level gate isn't in this binary's compile.
//
// Current gates:
//   - capabilities.tools — runtime gate at
//     internal/engine/engine.go:4459-4476 (PR7, commit 4b25f026).
//     Empty/missing capabilities.tools[] = runtime denies tool calls.
//   - delegation_allowlist — static delegation allowlist gate (with
//     swarm.Members[] shadowing per engine.go:2067-2123, post-
//     17b1731a swarm authority change).
var compiledInGates = []string{
	"capabilities.tools",
	"delegation_allowlist",
}

// BuildIdentity is the resolved identity of the running binary,
// folded from ldflags + VCS info + the compiled-in gates list.
// Exported so the buildStartupBanner consumer in serve.go and the
// internal package test can share the same shape.
type BuildIdentity struct {
	// Revision is the short-SHA of the source commit. Filled from
	// the ldflags commit var when non-default, otherwise the first
	// 7 chars of debug.BuildInfo's vcs.revision, otherwise "unknown".
	Revision string

	// BuildTime is the ISO-8601 timestamp the artifact was built.
	// Filled from the ldflags date var when non-default, otherwise
	// debug.BuildInfo's vcs.time, otherwise "unknown".
	BuildTime string

	// Dirty is true when the working tree was uncommitted at build
	// time (debug.BuildInfo's vcs.modified = "true"). When VCS info
	// is absent the field is false — the operator should treat
	// Revision="unknown" as the signal that dirty cannot be asserted.
	Dirty bool

	// GoVersion is the Go toolchain version the binary was compiled
	// with — runtime.Version() at process startup. The non-test call
	// site reads this from debug.BuildInfo.GoVersion when present;
	// the extractor falls through to runtime.Version() when the
	// BuildInfo is zero-valued.
	GoVersion string

	// Gates is the list of compiled-in fail-closed enforcement gates
	// (see compiledInGates). Stable shape: lower-snake-case tokens
	// the operator can grep against audit findings.
	Gates []string
}

// defaultLdflagVersion / defaultLdflagCommit / defaultLdflagDate are
// the package-level defaults set in cmd/flowstate/main.go when the
// build is NOT passing -ldflags overrides. The extractor treats a
// matching value as "ldflags not used; fall through to VCS".
const (
	defaultLdflagVersion = "dev"
	defaultLdflagCommit  = "unknown"
	defaultLdflagDate    = "unknown"
)

// extractBuildIdentity folds three sources into one BuildIdentity:
// the -ldflags -X package vars passed in by main.go, the
// runtime/debug.BuildInfo VCS settings, and the compiled-in gates
// constant. Pure function — no side effects, no I/O — so the spec
// can drive every branch.
//
// Expected:
//   - info is non-nil. The caller should pass the result of
//     runtime/debug.ReadBuildInfo() directly when ok=true, or a
//     zero-valued &debug.BuildInfo{} when ok=false. A nil info is
//     treated as a zero-valued one.
//   - versionVar, commitVar, dateVar are the values of
//     cmd/flowstate/main.go's `version`, `commit`, `date` package
//     vars at the call site. When the build was made without
//     -ldflags they will equal the defaults ("dev"/"unknown"/
//     "unknown") and the extractor falls through to VCS info.
//
// Returns:
//   - A BuildIdentity with every field populated. No field is left
//     as the empty string — when neither ldflags nor VCS supplies a
//     value the field becomes "unknown" so the banner can render an
//     honest gap rather than a confidently-wrong line.
//
// Side effects:
//   - None.
func extractBuildIdentity(info *debug.BuildInfo, versionVar, commitVar, dateVar string) BuildIdentity {
	if info == nil {
		info = &debug.BuildInfo{}
	}

	vcsRevision, vcsTime, dirty := readVCSSettings(info)

	revision := pickRevision(commitVar, vcsRevision)
	buildTime := pickBuildTime(dateVar, vcsTime)
	_ = versionVar // reserved — version is rendered by cli.SetVersion; banner pins SHA + time.

	goVersion := info.GoVersion
	if goVersion == "" {
		goVersion = runtime.Version()
	}

	gates := make([]string, len(compiledInGates))
	copy(gates, compiledInGates)

	return BuildIdentity{
		Revision:  revision,
		BuildTime: buildTime,
		Dirty:     dirty,
		GoVersion: goVersion,
		Gates:     gates,
	}
}

// readVCSSettings pulls the three VCS keys out of debug.BuildInfo
// settings. Returns ("", "", false) when any key is missing so the
// caller's fallback logic kicks in field-by-field.
func readVCSSettings(info *debug.BuildInfo) (revision, buildTime string, dirty bool) {
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			buildTime = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return revision, buildTime, dirty
}

// pickRevision returns the SHA to surface in the banner. ldflags
// commit wins when it isn't the cmd/flowstate/main.go default;
// otherwise the first 7 chars of the VCS revision; otherwise
// "unknown".
func pickRevision(ldflagsCommit, vcsRevision string) string {
	if ldflagsCommit != "" && ldflagsCommit != defaultLdflagCommit {
		return ldflagsCommit
	}
	if vcsRevision != "" {
		if len(vcsRevision) >= 7 {
			return vcsRevision[:7]
		}
		return vcsRevision
	}
	return "unknown"
}

// pickBuildTime returns the ISO-8601 build timestamp. ldflags date
// wins when it isn't the cmd/flowstate/main.go default; otherwise
// the VCS time; otherwise "unknown".
func pickBuildTime(ldflagsDate, vcsTime string) string {
	if ldflagsDate != "" && ldflagsDate != defaultLdflagDate {
		return ldflagsDate
	}
	if vcsTime != "" {
		return vcsTime
	}
	return "unknown"
}

// buildStartupBanner renders a BuildIdentity into the single-line
// stdout banner. The format is the key=value style the May 2026
// brief specifies — stable so the audit script can grep historical
// logs:
//
//	FlowState starting | sha=<rev> | built=<time> | dirty=<bool> | go=<version> | gates=<csv>
//
// Pure function — accepts a fully resolved BuildIdentity, returns
// the rendered string with no trailing newline (caller adds it).
//
// Expected:
//   - id is a fully populated BuildIdentity. The extractor guarantees
//     no field is empty in production; tests may pass partial values
//     to drive the rendering path.
//
// Returns:
//   - The rendered banner line, no trailing newline.
//
// Side effects:
//   - None.
func buildStartupBanner(id BuildIdentity) string {
	gates := strings.Join(id.Gates, ",")
	return fmt.Sprintf(
		"FlowState starting | sha=%s | built=%s | dirty=%t | go=%s | gates=%s",
		id.Revision, id.BuildTime, id.Dirty, id.GoVersion, gates,
	)
}

// resolveStartupBanner is the seam runServe calls to compose the
// banner. Reads runtime/debug.ReadBuildInfo() at the call site and
// folds in the cmd/flowstate/main.go ldflags package vars (passed in
// by the SetVersion wiring at startup). Kept tiny so runServe stays
// readable; the load-bearing branches are all in extractBuildIdentity
// and buildStartupBanner.
//
// Expected:
//   - ldflagsVersion, ldflagsCommit, ldflagsDate may be the
//     cmd/flowstate/main.go defaults when build was made without
//     -ldflags overrides; the extractor falls through to VCS info.
//
// Returns:
//   - The rendered banner line ready for fmt.Fprintln to stdout.
//
// Side effects:
//   - Calls runtime/debug.ReadBuildInfo() — pure read of the
//     statically-embedded build settings.
func resolveStartupBanner(ldflagsVersion, ldflagsCommit, ldflagsDate string) string {
	info, _ := debug.ReadBuildInfo() // ok=false → nil, extractor tolerates.
	id := extractBuildIdentity(info, ldflagsVersion, ldflagsCommit, ldflagsDate)
	return buildStartupBanner(id)
}
