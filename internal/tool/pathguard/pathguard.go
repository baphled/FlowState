// Package pathguard provides path-based access control for file-accessing tools.
//
// This package enforces deny-list restrictions so agents cannot read or write
// sensitive directories (vaults, config) through file tools or bash commands.
// MCP tools bypass these restrictions by design — they are the intended access
// path for protected data.
package pathguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/baphled/flowstate/internal/permissionmode"
	"github.com/baphled/flowstate/internal/session"
)

// PermissionsMatcher is the minimal interface pathguard needs from a
// tool-scoped permissions matcher. The concrete implementation lives in
// internal/config (config.Permissions). The interface is declared here
// to keep pathguard zero-dep on the config package and to give callers
// an easy seam for tests or alternative matchers.
//
// The contract mirrors config.Permissions.Match:
//
//   - ("deny",  true)  → caller MUST deny the access.
//   - ("allow", true)  → caller MAY allow the access (and SHOULD short
//     circuit the legacy deny-list check).
//   - ("",      false) → matcher has no opinion; caller falls through
//     to the legacy Check / CheckCommand semantics.
type PermissionsMatcher interface {
	Match(tool, path string) (decision string, matched bool)
}

// PermissionRequest carries the metadata pathguard hands to the
// prompter when it would otherwise return an access-denied error AND
// the session is in ModeAskUser. The prompter (app.go's
// implementation) publishes EventPermissionRequired and blocks on
// permissionrequest.Registry.Wait until the operator answers or the
// 5-minute timeout fires. Permission Mode ModeAskUser Extension plan
// (May 2026) Slice 2.
//
// SessionID is sourced from session.IDKey on the tool-invocation ctx
// when available; empty when not (e.g. test harnesses without a
// session stamp). The prompter SHOULD NOT block when SessionID is
// empty — that's a wiring bug.
type PermissionRequest struct {
	ToolName     string
	Resource     string
	AgentName    string
	DenialReason string
	SessionID    string
	Mode         string
}

// GrantScope is the pathguard-local mirror of permissionrequest.Scope.
// Kept as a local enum so pathguard stays zero-dep on the registry
// package — the prompter (defined elsewhere) does the registry talk;
// pathguard just consumes the returned scope.
type GrantScope string

const (
	// GrantOnce permits the suspended call only. No persistence.
	GrantOnce GrantScope = "once"
	// GrantSession appends the resource to the per-session in-memory
	// allow set. Second call to the same (tool, resource) returns
	// nil without re-consulting the prompter.
	GrantSession GrantScope = "session"
	// GrantForever appends to permissions.yaml via the writer landed
	// in Slice 4. Slice 2 treats Forever identically to Session
	// (in-memory only) so the wire shape is stable while Slice 4
	// lands.
	GrantForever GrantScope = "forever"
	// GrantDeny resumes the call with the existing access-denied
	// error path.
	GrantDeny GrantScope = "deny"
)

// PermissionGrant is the prompter's return shape. Scope drives the
// pathguard effect; Err is the access-denied error to surface when
// Scope == GrantDeny (lets the prompter customise the message; an
// empty Err on GrantDeny falls back to the original denial reason).
type PermissionGrant struct {
	Scope GrantScope
	Err   error
}

// PermissionPrompter is the seam pathguard uses to escalate a denial
// to the operator under ModeAskUser. The implementation in app.go
// publishes EventPermissionRequired and blocks on the
// permissionrequest.Registry until the operator answers.
//
// Contract:
//
//   - ctx is the tool-invocation ctx. Implementations MUST detach it
//     from the parent (context.WithoutCancel) before passing it to
//     the registry's Wait so a tab-close does not cancel the suspension
//     before the operator can grant. Memory:
//     project_flowstate_streamer_request_lifetime_coupling.
//
//   - The returned grant determines the pathguard effect. Implementations
//     MUST return GrantDeny on timeout, NOT a synthetic error — the
//     caller is the layer that surfaces the denial to the model.
type PermissionPrompter interface {
	RequestPermission(ctx context.Context, req PermissionRequest) PermissionGrant
}

// Guard checks filesystem paths against a deny list, optionally
// consulting a per-tool PermissionsMatcher first.
//
// planOutputDir, when non-empty, scopes Plan-mode file-mutation tools
// (write/edit/multiedit/apply_patch) to paths under it. Under Plan
// mode the operator-supplied allow rules in PermissionsMatcher for
// those four tools are REPLACED by "path must be under
// planOutputDir"; the matcher's deny rules still apply (deny wins
// over allow, mirroring config.Permissions.Match precedence at
// permissions.go:127-129).
//
// Plan-Mode Output Directory plan (May 2026) §3 Slice 1.
type Guard struct {
	denied        []string
	perms         PermissionsMatcher
	planOutputDir string
	// prompter, when non-nil, is consulted on a denial path when ctx
	// carries ModeAskUser. nil collapses to the binary deny semantics.
	// Permission Mode ModeAskUser Extension plan (May 2026) Slice 2.
	prompter PermissionPrompter
	// sessionAllowMu guards sessionAllow.
	sessionAllowMu sync.Mutex
	// sessionAllow tracks per-session GrantSession decisions —
	// sessionID → (tool|resource) → bool. A subsequent call with the
	// same (tool, resource) under the same session returns nil
	// without re-consulting the prompter. In-memory only; clears
	// when the engine evicts the session.
	sessionAllow map[string]map[string]bool
}

// planScopedTools is the set of file-mutating tools that pathguard's
// Plan-mode overlay re-scopes to planOutputDir. It mirrors
// permissionmode.MutatingTools minus bash (bash is fully stripped at
// the engine schema layer; pathguard never sees a Plan-mode bash call
// because the schema filter removes it before dispatch).
//
// Kept in this package rather than imported from permissionmode to
// preserve pathguard's "consumer of permissionmode for the ctx key
// only" stance — the strip-vs-scope split is a pathguard concern.
var planScopedTools = map[string]struct{}{
	"write":       {},
	"edit":        {},
	"multiedit":   {},
	"apply_patch": {},
}

// New creates a Guard that blocks access to any path under the given denied
// directories. Each entry must be an absolute path; relative paths are
// silently ignored. Nil or empty denied is a no-op (all paths allowed).
//
// The returned Guard has no PermissionsMatcher attached — all *ForTool
// methods fall straight through to the legacy Check / CheckCommand
// logic. Use NewWithPermissions to wire a matcher in.
func New(denied []string) *Guard {
	abs := normaliseDenied(denied)
	return &Guard{denied: abs}
}

// NewWithPermissions creates a Guard that consults perms first via the
// *ForTool methods, then falls through to the legacy denied-roots
// check when perms has no opinion. perms may be nil, in which case
// behaviour matches New(denied).
func NewWithPermissions(denied []string, perms PermissionsMatcher) *Guard {
	abs := normaliseDenied(denied)
	return &Guard{denied: abs, perms: perms}
}

// NewWithPermissionsAndPlanOutputDir creates a Guard wired with both a
// PermissionsMatcher and the operator's resolved plan_output_dir. Under
// Plan mode, the four file-mutating tools (write/edit/multiedit/
// apply_patch) are constrained to paths inside planOutputDir regardless
// of the matcher's allow rules; the matcher's DENY rules still fire so
// "deny wins over allow" precedence is preserved.
//
// Empty planOutputDir collapses to NewWithPermissions semantics — the
// Plan-mode overlay is a no-op and the call falls through to the
// matcher / legacy guard. This is the safe fall-back when XDG resolution
// failed at bootstrap time; Plan-mode writes then fail closed via the
// matcher (no allow rule matches the path).
func NewWithPermissionsAndPlanOutputDir(denied []string, perms PermissionsMatcher, planOutputDir string) *Guard {
	abs := normaliseDenied(denied)
	g := &Guard{denied: abs, perms: perms}
	if planOutputDir != "" {
		if absPath, err := filepath.Abs(planOutputDir); err == nil {
			g.planOutputDir = absPath
		}
	}
	return g
}

// SetPermissionPrompter wires a PermissionPrompter onto an existing
// Guard. nil unwires the prompter — the Guard reverts to binary deny
// semantics. Permission Mode ModeAskUser Extension plan (May 2026)
// Slice 2.
//
// The setter shape (rather than a fresh constructor) avoids a
// combinatorial explosion of New variants: the existing constructors
// already handle the matcher / plan-output-dir / denied-roots cross
// product. The prompter is orthogonal and is wired by app.go after
// the Guard is constructed.
func (g *Guard) SetPermissionPrompter(p PermissionPrompter) {
	g.prompter = p
}

// rememberSessionAllow records that the supplied (tool, resource)
// pair has been granted GrantSession scope for sessionID. The
// per-session map is allocated lazily so the cold path stays
// allocation-free.
func (g *Guard) rememberSessionAllow(sessionID, tool, resource string) {
	if sessionID == "" {
		return
	}
	g.sessionAllowMu.Lock()
	defer g.sessionAllowMu.Unlock()
	if g.sessionAllow == nil {
		g.sessionAllow = make(map[string]map[string]bool)
	}
	bySession, ok := g.sessionAllow[sessionID]
	if !ok {
		bySession = make(map[string]bool)
		g.sessionAllow[sessionID] = bySession
	}
	bySession[sessionAllowKey(tool, resource)] = true
}

// isSessionAllowed reports whether (tool, resource) has been granted
// GrantSession scope for sessionID. False when sessionID is empty
// (no session ⇒ no per-session memory).
func (g *Guard) isSessionAllowed(sessionID, tool, resource string) bool {
	if sessionID == "" {
		return false
	}
	g.sessionAllowMu.Lock()
	defer g.sessionAllowMu.Unlock()
	bySession, ok := g.sessionAllow[sessionID]
	if !ok {
		return false
	}
	return bySession[sessionAllowKey(tool, resource)]
}

// ClearSessionAllow drops the per-session in-memory allow set for
// sessionID. Called by the session-ended event subscriber so the
// pathguard does not retain grants beyond the session lifetime.
func (g *Guard) ClearSessionAllow(sessionID string) {
	if sessionID == "" {
		return
	}
	g.sessionAllowMu.Lock()
	defer g.sessionAllowMu.Unlock()
	delete(g.sessionAllow, sessionID)
}

func sessionAllowKey(tool, resource string) string {
	return tool + "\x00" + resource
}

func normaliseDenied(denied []string) []string {
	abs := make([]string, 0, len(denied))
	for _, d := range denied {
		if d == "" {
			continue
		}
		a, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		abs = append(abs, a)
	}
	return abs
}

// Check returns an error when path resolves inside any denied directory.
// If the current working directory is itself inside a denied directory,
// the check passes (the user chose to work inside that directory).
//
// Check is equivalent to CheckForTool("", path) — it never consults the
// PermissionsMatcher and uses only the legacy denied-roots semantics.
func (g *Guard) Check(path string) error {
	if len(g.denied) == 0 {
		return nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	cwd, _ := os.Getwd()
	for _, d := range g.denied {
		if cwd != "" && strings.HasPrefix(cwd, d+string(filepath.Separator)) {
			continue
		}
		if strings.HasPrefix(abs, d+string(filepath.Separator)) || abs == d {
			return fmt.Errorf("access denied: %s is a protected path (use the appropriate MCP tool)", path)
		}
	}
	return nil
}

// CheckCommand scans a bash command string for unquoted argv tokens that
// resolve under a denied directory.
//
// CheckCommand is equivalent to CheckCommandForTool("", command) — it
// never consults the PermissionsMatcher and uses only the legacy
// denied-roots semantics.
//
// The implementation tokenises the command in a quote-aware way: text
// enclosed in single or double quotes (including heredoc bodies on
// separate lines) is exempt because it cannot be a path argument the
// shell will hand to a file-touching syscall. Comments (everything after
// an unquoted `#`) are stripped. The remaining tokens are tested only
// when they LOOK like a filesystem path — starting with `/`, `~`,
// `./`, `../`, or containing a `/`. Bare words like `vault` or
// `baphled` are never flagged.
//
// The cwd carve-out from Check applies here too: when the working
// directory is inside a denied root, the user is intentionally working
// in that tree and commands referring to it are allowed.
func (g *Guard) CheckCommand(command string) error {
	if len(g.denied) == 0 {
		return nil
	}

	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	for _, tok := range tokenize(command) {
		if !looksLikePath(tok) {
			continue
		}

		expanded := expandHome(tok, home)
		abs, err := filepath.Abs(expanded)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)

		for _, d := range g.denied {
			if cwd != "" && strings.HasPrefix(cwd, d+string(filepath.Separator)) {
				continue
			}
			if strings.HasPrefix(abs, d+string(filepath.Separator)) || abs == d {
				return fmt.Errorf("access denied: command references protected path %s (use the appropriate MCP tool)", d)
			}
		}
	}
	return nil
}

// tokenize splits a bash-ish command into argv-like tokens. It is
// deliberately simpler than a real shell parser:
//
//   - text inside single or double quotes is treated as one opaque
//     chunk that DOES NOT contribute a candidate path (the shell will
//     hand the string to the command verbatim — it is not interpreted
//     as a filesystem reference by the shell itself);
//   - everything after an unquoted `#` to end-of-line is a comment and
//     dropped;
//   - tokens are separated by ASCII whitespace plus the shell
//     redirection metacharacters `<`, `>`, `|`, `&`, `;`, `(`, `)`,
//     backtick, equals — splitting at these lets `cmd >foo` and
//     `K=foo` surface `foo` as its own token without the operator
//     glued to it.
//
// The tokeniser does not attempt to expand variables, command
// substitution, or globs; the goal is "produce candidate path
// arguments" not "produce a faithful argv". Quoted heredoc bodies
// (cat <<EOF … EOF) inherit the quote-skip rule via the leading `"`
// or `'` of the delimiter or via the absence of a path-shaped first
// character — in the common `<<EOF\nbody\nEOF` shape there is no
// quote, so we instead skip any line that follows a heredoc marker
// until we see the marker again. That heredoc handling is sufficient
// to kill the most common false-positive case; rare exotic
// constructs may still leak a token, which is acceptable given the
// fallback Check on the actual filesystem call.
func tokenize(command string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	skipHeredoc := false
	heredocMarker := ""

	lines := strings.Split(command, "\n")
	for lineIdx, line := range lines {
		if skipHeredoc {
			if strings.TrimSpace(line) == heredocMarker {
				skipHeredoc = false
				heredocMarker = ""
			}
			continue
		}

		flush := func() {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}

		i := 0
		for i < len(line) {
			c := line[i]
			switch {
			case inSingle:
				if c == '\'' {
					inSingle = false
					current.Reset() // discard the contents of a single-quoted run entirely
				}
				i++
			case inDouble:
				if c == '"' {
					inDouble = false
					current.Reset() // discard double-quoted run too
				}
				i++
			case c == '\'':
				flush()
				inSingle = true
				i++
			case c == '"':
				flush()
				inDouble = true
				i++
			case c == '#':
				// unquoted comment to end of line
				flush()
				i = len(line)
			case c == '<' && i+1 < len(line) && line[i+1] == '<':
				// heredoc — consume the marker word on this line, then
				// skip subsequent lines until the marker re-appears.
				flush()
				i += 2
				// skip leading `-` (<<-EOF strips tabs but the marker text is the same)
				if i < len(line) && line[i] == '-' {
					i++
				}
				// skip whitespace
				for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
					i++
				}
				marker := strings.Builder{}
				for i < len(line) {
					mc := line[i]
					if mc == ' ' || mc == '\t' || mc == ';' || mc == '|' || mc == '&' {
						break
					}
					if mc == '\'' || mc == '"' || mc == '\\' {
						i++
						continue
					}
					marker.WriteByte(mc)
					i++
				}
				heredocMarker = marker.String()
				if heredocMarker != "" {
					skipHeredoc = true
				}
			case isSeparator(c):
				flush()
				i++
			default:
				current.WriteByte(c)
				i++
			}
		}
		flush()
		_ = lineIdx
	}

	return tokens
}

// isSeparator reports whether a byte ends the current token. This is a
// superset of POSIX shell whitespace plus the redirection /
// command-grouping metacharacters that the shell uses to delimit words.
func isSeparator(c byte) bool {
	switch c {
	case ' ', '\t', '\r':
		return true
	case '|', '&', ';', '(', ')', '<', '>', '`', '=':
		return true
	}
	return false
}

// looksLikePath returns true when tok looks like a filesystem path the
// shell would hand to a file-touching syscall. A bare word with no `/`
// and no leading `.` or `~` is treated as a command name or argument
// value, not a path.
func looksLikePath(tok string) bool {
	if tok == "" {
		return false
	}
	if strings.HasPrefix(tok, "/") {
		return true
	}
	if strings.HasPrefix(tok, "~") {
		return true
	}
	if strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") {
		return true
	}
	if tok == "." || tok == ".." {
		return true
	}
	if strings.Contains(tok, "/") {
		return true
	}
	return false
}

// CheckForTool first inspects the session's permission mode on ctx —
// when the mode is YOLO the call short-circuits to PASS before any
// matcher / denied-roots check runs. Otherwise it consults the
// configured PermissionsMatcher for an allow / deny verdict on
// (tool, path), then falls through to the legacy Check semantics when
// the matcher has no opinion.
//
// Decision flow (mode != YOLO):
//   - matcher returns "deny"  → returns an access-denied error and
//     does NOT consult the legacy denied roots.
//   - matcher returns "allow" → returns nil and does NOT consult the
//     legacy denied roots (the explicit allow grants the access).
//   - matcher returns ""      → defers entirely to Check(path).
//
// A nil matcher (or a Guard built via New) collapses to Check(path).
// An empty tool name behaves the same: the matcher has no entry, so
// Check(path) runs.
//
// The ctx parameter MUST be the tool-invocation ctx (the same ctx
// handed to Tool.Execute by the engine's dispatch path); a missing
// mode binding canonicalises to "default" via
// permissionmode.FromContext so legacy callers that have not yet
// wired the engine seam still get the safe pre-Permission-Modes
// behaviour. Permission Modes plan §4 Slice 1.
func (g *Guard) CheckForTool(ctx context.Context, tool, path string) error {
	mode := permissionmode.FromContext(ctx)
	if mode == permissionmode.ModeYolo {
		return nil
	}

	// Plan-Mode Output Directory overlay (Plan §3 Slice 1):
	// for the four file-mutating tools under Plan mode, replace the
	// matcher's allow rules with "path must be under planOutputDir".
	// The matcher's deny rules still apply — config.Permissions.Match
	// returns "deny" first when both match (permissions.go:127-129),
	// so deny-wins-over-allow precedence is preserved.
	if mode == permissionmode.ModePlan {
		if _, scoped := planScopedTools[tool]; scoped {
			return g.checkPlanModeScoped(tool, path)
		}
	}

	// Permission Mode ModeAskUser Extension plan (May 2026) Slice 2.
	// Under ModeAskUser, a per-session allow set short-circuits the
	// matcher / denied-roots checks for resources the operator has
	// already granted Session scope on. The check happens before the
	// matcher consultation so a GrantSession holds against any matcher
	// deny rule the operator has explicitly overridden for this
	// session. Outside ModeAskUser the allow set is ignored — the
	// short-circuit is opt-in via the mode dial.
	if mode == permissionmode.ModeAskUser {
		sessionID := pathguardSessionID(ctx)
		if g.isSessionAllowed(sessionID, tool, path) {
			return nil
		}
	}

	denial := g.computeDenial(tool, path)
	if denial == nil {
		return nil
	}

	// Ask-user escalation: prompter consulted ONLY when the mode is
	// ModeAskUser AND a prompter is wired. Outside this branch the
	// denial flows through to the caller untouched — binary semantics
	// preserved for Default / AcceptEdits / Plan.
	if mode != permissionmode.ModeAskUser || g.prompter == nil {
		return denial
	}
	return g.escalateForTool(ctx, tool, path, denial)
}

// computeDenial evaluates the matcher / denied-roots pipeline and
// returns the access-denied error (or nil for permitted). Extracted
// from CheckForTool so the ask-user escalation site can decide
// independently of the underlying decision flow.
func (g *Guard) computeDenial(tool, path string) error {
	if g.perms != nil && tool != "" {
		abs, err := filepath.Abs(path)
		if err == nil {
			if decision, matched := g.perms.Match(tool, abs); matched {
				switch decision {
				case "deny":
					return fmt.Errorf("access denied: %s is blocked by the %q tool's permissions config", path, tool)
				case "allow":
					return nil
				}
			}
		}
	}
	return g.Check(path)
}

// escalateForTool publishes a permission request to the prompter,
// applies the returned grant, and returns the resulting pathguard
// effect. GrantOnce / Session / Forever all return nil (the call
// proceeds); GrantSession also memos the (tool, resource) pair so a
// follow-up call within the session bypasses the prompter. GrantDeny
// returns the original denial (or the prompter-supplied error if
// non-nil).
func (g *Guard) escalateForTool(ctx context.Context, tool, path string, denial error) error {
	sessionID := pathguardSessionID(ctx)
	agentName := pathguardAgentName(ctx)
	req := PermissionRequest{
		ToolName:     tool,
		Resource:     path,
		AgentName:    agentName,
		DenialReason: denial.Error(),
		SessionID:    sessionID,
		Mode:         permissionmode.ModeAskUser,
	}
	grant := g.prompter.RequestPermission(ctx, req)
	switch grant.Scope {
	case GrantOnce:
		return nil
	case GrantSession:
		g.rememberSessionAllow(sessionID, tool, path)
		return nil
	case GrantForever:
		// Slice 4 wires the permissions.yaml writer; for now treat
		// Forever identically to Session so the wire shape is stable
		// and the operator's intent is honoured for the rest of the
		// session.
		g.rememberSessionAllow(sessionID, tool, path)
		return nil
	case GrantDeny:
		if grant.Err != nil {
			return grant.Err
		}
		return denial
	default:
		// Unknown scope — fail closed to the original denial. The
		// prompter is in-tree; this branch is defensive against
		// future enum additions that forget to update this switch.
		return denial
	}
}

// checkPlanModeScoped applies the Plan-mode overlay for a single
// path-shaped argument: the file mutation is permitted only when the
// path resolves inside planOutputDir AND the configured matcher does
// not deny it. The matcher's allow rules are intentionally NOT
// consulted — the overlay replaces them entirely so the operator-set
// vault root cannot accidentally widen Plan-mode write access.
//
// Returns:
//   - nil when the absolute path resolves under planOutputDir and the
//     matcher has no deny verdict.
//   - An access-denied error when planOutputDir is empty, when the
//     path resolves outside planOutputDir, or when the matcher
//     explicitly denies the path.
//
// Side effects:
//   - None.
func (g *Guard) checkPlanModeScoped(tool, path string) error {
	if g.planOutputDir == "" {
		return fmt.Errorf("access denied: Plan mode requires plan_output_dir to be configured (%q tool blocked: %s)", tool, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("access denied: %s is not under plan_output_dir (%q tool, Plan mode)", path, tool)
	}
	if !isUnderDir(abs, g.planOutputDir) {
		return fmt.Errorf("access denied: %s is not under plan_output_dir %s (%q tool, Plan mode)", abs, g.planOutputDir, tool)
	}
	// Deny rules still apply — e.g. .obsidian under the plan_output_dir.
	if g.perms != nil {
		if decision, matched := g.perms.Match(tool, abs); matched && decision == "deny" {
			return fmt.Errorf("access denied: %s is blocked by the %q tool's permissions config (Plan mode)", abs, tool)
		}
	}
	return nil
}

// isUnderDir reports whether abs (a cleaned absolute path) resolves
// inside dir (also expected absolute). Both equal-to-dir and a strict
// descendant return true; the function is used to scope Plan-mode
// writes to the operator's plan_output_dir.
func isUnderDir(abs, dir string) bool {
	if abs == dir {
		return true
	}
	return strings.HasPrefix(abs, dir+string(filepath.Separator))
}

// CheckCommandForTool tokenises command and consults the configured
// PermissionsMatcher for each path-shaped token under (tool, <token>),
// then falls back to the legacy CheckCommand semantics for any token
// the matcher had no opinion on. When ctx carries permission mode
// YOLO the call short-circuits to PASS before tokenisation runs.
//
// Per-token decision flow (mode != YOLO):
//   - matcher returns "deny"  → returns an access-denied error
//     immediately.
//   - matcher returns "allow" → that token is exempt from the legacy
//     denied-roots check; evaluation continues with the next token.
//   - matcher returns ""      → that token still gets the legacy
//     denied-roots check (mirroring the in-place CheckCommand logic).
//
// A nil matcher (or empty tool name) collapses to CheckCommand(command).
//
// The ctx parameter MUST be the tool-invocation ctx (the same ctx
// handed to Tool.Execute by the engine's dispatch path); a missing
// mode binding canonicalises to "default" via
// permissionmode.FromContext so legacy callers that have not yet
// wired the engine seam still get the safe pre-Permission-Modes
// behaviour. Permission Modes plan §4 Slice 1.
func (g *Guard) CheckCommandForTool(ctx context.Context, tool, command string) error {
	mode := permissionmode.FromContext(ctx)
	if mode == permissionmode.ModeYolo {
		return nil
	}

	// Plan-mode overlay for command-shaped tool invocations. In
	// practice the engine schema strips bash before dispatch under
	// Plan mode (permissionmode.PlanModeStrippedTools), so this code
	// path is defence-in-depth for any future command-style file
	// tool that lands in planScopedTools — each path-shaped token
	// must resolve under planOutputDir.
	if mode == permissionmode.ModePlan {
		if _, scoped := planScopedTools[tool]; scoped {
			return g.checkPlanModeCommand(tool, command)
		}
	}

	denial, deniedResource := g.computeCommandDenial(tool, command, mode)
	if denial == nil {
		return nil
	}

	if mode != permissionmode.ModeAskUser || g.prompter == nil {
		return denial
	}
	return g.escalateForTool(ctx, tool, deniedResource, denial)
}

// computeCommandDenial evaluates the tokenised command against the
// matcher + denied-roots and returns the first denial encountered
// together with the resource string that triggered it. Returns (nil,
// "") when every path-shaped token passes.
//
// Extracted from CheckCommandForTool so the ask-user escalation site
// can decide independently of the underlying decision flow. Under
// ModeAskUser the per-session allow set short-circuits the matcher
// per token; outside ModeAskUser the allow set is ignored.
func (g *Guard) computeCommandDenial(tool, command string, mode permissionmode.Mode) (error, string) {
	if g.perms == nil || tool == "" {
		if err := g.CheckCommand(command); err != nil {
			return err, command
		}
		return nil, ""
	}

	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	_ = mode // session-allow consultation handled by the caller via
	// the rememberSessionAllow / isSessionAllowed surface — keep
	// computeCommandDenial mode-agnostic so the escalation site is
	// the single source of truth for the ask-user branch.

	for _, tok := range tokenize(command) {
		if !looksLikePath(tok) {
			continue
		}

		expanded := expandHome(tok, home)
		abs, err := filepath.Abs(expanded)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)

		if decision, matched := g.perms.Match(tool, abs); matched {
			switch decision {
			case "deny":
				return fmt.Errorf("access denied: %s is blocked by the %q tool's permissions config", abs, tool), abs
			case "allow":
				continue // exempt from legacy check
			}
		}

		// Matcher had no opinion — fall through to the legacy
		// denied-roots check for this token only.
		if len(g.denied) == 0 {
			continue
		}
		for _, d := range g.denied {
			if cwd != "" && strings.HasPrefix(cwd, d+string(filepath.Separator)) {
				continue
			}
			if strings.HasPrefix(abs, d+string(filepath.Separator)) || abs == d {
				return fmt.Errorf("access denied: command references protected path %s (use the appropriate MCP tool)", d), abs
			}
		}
	}
	return nil, ""
}

// pathguardSessionID extracts the active session ID from the tool-
// invocation ctx using the canonical session.IDKey{}. Empty when no
// session is stamped (test harnesses, legacy call sites).
func pathguardSessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(session.IDKey{}).(string)
	return v
}

// pathguardAgentName extracts the per-turn agent override (set by the
// engine's delegation / @-mention paths) from ctx. Empty when no
// override is present, which is the dominant case for sessions
// driven by their persistent agent_id. Best-effort: the prompter
// uses the value for diagnostic stamping only.
func pathguardAgentName(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return session.StreamAgentOverrideFromContext(ctx)
}

// checkPlanModeCommand applies the Plan-mode overlay to a tokenised
// command. Every path-shaped token must resolve inside planOutputDir
// and survive the matcher's deny check. See checkPlanModeScoped for
// the per-path semantics.
//
// Returns:
//   - nil when planOutputDir is set and every path-shaped token is
//     under it (and not matcher-denied).
//   - An access-denied error otherwise.
//
// Side effects:
//   - Reads $HOME / cwd via os.UserHomeDir / os.Getwd (mirrors the
//     legacy CheckCommand tokenisation path).
func (g *Guard) checkPlanModeCommand(tool, command string) error {
	if g.planOutputDir == "" {
		return fmt.Errorf("access denied: Plan mode requires plan_output_dir to be configured (%q tool blocked)", tool)
	}
	home, _ := os.UserHomeDir()
	for _, tok := range tokenize(command) {
		if !looksLikePath(tok) {
			continue
		}
		expanded := expandHome(tok, home)
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return fmt.Errorf("access denied: command token %q is not under plan_output_dir (%q tool, Plan mode)", tok, tool)
		}
		abs = filepath.Clean(abs)
		if !isUnderDir(abs, g.planOutputDir) {
			return fmt.Errorf("access denied: %s is not under plan_output_dir %s (%q tool, Plan mode)", abs, g.planOutputDir, tool)
		}
		if g.perms != nil {
			if decision, matched := g.perms.Match(tool, abs); matched && decision == "deny" {
				return fmt.Errorf("access denied: %s is blocked by the %q tool's permissions config (Plan mode)", abs, tool)
			}
		}
	}
	return nil
}

// expandHome replaces a leading `~` or `$HOME` with the supplied home
// directory. It is intentionally narrow — only the leading form is
// expanded so `~user` or mid-token `$HOME` references are left alone.
func expandHome(tok, home string) string {
	if home == "" {
		return tok
	}
	if tok == "~" {
		return home
	}
	if strings.HasPrefix(tok, "~/") {
		return home + tok[1:]
	}
	if strings.HasPrefix(tok, "$HOME/") {
		return home + tok[len("$HOME"):]
	}
	if tok == "$HOME" {
		return home
	}
	return tok
}
