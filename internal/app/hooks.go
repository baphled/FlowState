package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrHookPointUnsupported is the config error (hook_point_unsupported) for a
// point this build cannot fire where the hook sits: a point no packet has made
// fireable yet or transform before it lands.
var ErrHookPointUnsupported = errors.New("hook_point_unsupported")

// AcceptedHooks is the set of points this build fires: tool.before as a gate, tool.after as an observer and
// the transform phase (ADR-012 packet 3b; runCall builds its hookGate) and, on a root session run or serve
// (BeginRootRunHooks), user.prompt, session.start and session.end. A file that names any other point fails
// to load until the packet that fires it adds it here. A child or a swarm coordinator fires none of the
// three run-level points (ADR-012 gap 8).
func AcceptedHooks() HookAccepted {
	return HookAccepted{Points: []HookPoint{HookToolBefore, HookToolAfter, HookUserPrompt, HookSessionStart, HookSessionEnd}, Transform: true}
}

// ErrHookScopeInvalid is returned by LoadHooks for a scope outside user,
// project, both and none. The empty scope is invalid too: it means the caller
// never parsed --hooks, whose default (user) ParseHookScope applies.
var ErrHookScopeInvalid = errors.New("hook_scope_invalid")

// ParseHookScope reads a --hooks value; the empty string is HookScopeUser.
func ParseHookScope(s string) (HookScope, error) {
	switch HookScope(s) {
	case "", HookScopeUser:
		return HookScopeUser, nil
	case HookScopeProject, HookScopeBoth, HookScopeNone:
		return HookScope(s), nil
	}
	return "", fmt.Errorf("%w: --hooks: %q (want user, project, both or none)", ErrUsage, s)
}

// UserHooksPath is flowstate/hooks.json in the OS user config directory.
func UserHooksPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("%w: cannot locate the user config directory: %w", ErrConfigInvalid, err)
	}
	return filepath.Join(cfg, "flowstate", "hooks.json"), nil
}

// ProjectHooksPath is .flowstate/hooks.json below the project root; an empty
// root is the process's current directory. Callers pass req.ProjectRoot
// directly, never the projectRoot(req) helper in execloop_session.go: that
// helper prefers the per-run --workdir, which ADR-012 D3 forbids here.
func ProjectHooksPath(projectRoot string) string {
	if projectRoot == "" {
		projectRoot = "."
	}
	return filepath.Join(projectRoot, ".flowstate", "hooks.json")
}

// LoadHooks builds the effective hook set for the scope: the user file in file
// order, then the project file. A file the scope does not select is never
// opened. An id found twice in the loaded set, across both files, is a load
// error naming both origins. A scope outside the four values (the empty scope
// included) is ErrHookScopeInvalid, also config_invalid. Every failure is
// config_invalid; none of this runs a hook. The set hash and profile hooks are the next packet.
func LoadHooks(req HooksRequest) (LoadedHooks, error) {
	switch req.Scope {
	case HookScopeUser, HookScopeProject, HookScopeBoth, HookScopeNone:
	default:
		return LoadedHooks{}, fmt.Errorf("%w: %w: %q (want user, project, both or none)", ErrConfigInvalid, ErrHookScopeInvalid, req.Scope)
	}
	out := LoadedHooks{Scope: req.Scope, Hooks: []Hook{}}
	if req.Scope == HookScopeUser || req.Scope == HookScopeBoth {
		path, err := UserHooksPath()
		if err != nil {
			return LoadedHooks{}, err
		}
		if out.Hooks, err = appendFileHooks(out.Hooks, path, HookSourceUser, req.Accepted); err != nil {
			return LoadedHooks{}, err
		}
	}
	if req.Scope == HookScopeProject || req.Scope == HookScopeBoth {
		var err error
		path := ProjectHooksPath(req.ProjectRoot)
		if out.Hooks, err = appendFileHooks(out.Hooks, path, HookSourceProject, req.Accepted); err != nil {
			return LoadedHooks{}, err
		}
	}
	if err := checkDuplicateIDs(out.Hooks); err != nil {
		return LoadedHooks{}, err
	}
	return out, nil
}

// Digest is the hex SHA-256 of the effective set, the composition pin hooks.loaded records (ADR-012): the
// canonical JSON of every hook in firing order, which names its id, point, matchers, kind, argv, resolved
// executable path, environment names, timeout and failure policy. The scope is recorded beside it, not in it.
func (l LoadedHooks) Digest() string {
	hooks := l.Hooks
	if hooks == nil {
		hooks = []Hook{}
	}
	encoded, err := json.Marshal(hooks)
	if err != nil { // unreachable: Hook holds only strings, numbers and bools
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// appendFileHooks reads one hook file, if it exists, and appends its hooks.
func appendFileHooks(dst []Hook, path string, source HookSource, accepted HookAccepted) ([]Hook, error) {
	data, found, err := readHookFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrConfigInvalid, path, err)
	}
	if !found {
		return dst, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrConfigInvalid, path, err)
	}
	hooks, err := parseHookDocument(data, hookContext{accepted: accepted, dir: filepath.Dir(abs), origin: path,
		source: source})
	if err != nil {
		return nil, err
	}
	return append(dst, hooks...), nil
}

// checkDuplicateIDs refuses an id that appears twice in the loaded set.
func checkDuplicateIDs(hooks []Hook) error {
	first := make(map[string]Hook, len(hooks))
	for _, h := range hooks {
		if prev, dup := first[h.ID]; dup {
			return fmt.Errorf("%w: hook id %q is declared in both %s and %s", ErrConfigInvalid, h.ID, prev.Origin, h.Origin)
		}
		first[h.ID] = h
	}
	return nil
}
