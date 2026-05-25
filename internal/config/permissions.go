package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Permissions is the tool-scoped permissions configuration loaded from
// the permissions YAML file. It is the foundation for the Tool-Scoped
// Permissions plan (Slice A): no caller currently routes through it, so
// loading and parsing it has no behavioural effect end-to-end. Later
// slices wire it into pathguard and the engine's tool dispatch.
//
// Schema:
//
//	version: 1
//	tools:
//	  <tool-name>:
//	    allow: [<glob>, ...]
//	    deny:  [<glob>, ...]
//
// Globs use the doublestar dialect (full ** recursion). Allow and deny
// are independent — deny always wins when both match.
type Permissions struct {
	Version int                  `yaml:"version" json:"version"`
	Tools   map[string]ToolRules `yaml:"tools" json:"tools"`
}

// ToolRules holds the allow and deny glob lists for a single tool.
// Empty slices mean "no opinion from this tool entry"; the caller
// must then fall through to the legacy VaultPath / pathguard check.
type ToolRules struct {
	Allow []string `yaml:"allow,omitempty" json:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty" json:"deny,omitempty"`
}

// supportedPermissionsVersion is the highest schema version this build
// understands. Files declaring a higher version are treated as absent
// (forward-compat hatch per the plan) — the legacy guard remains in
// effect and a slog warning surfaces the mismatch.
const supportedPermissionsVersion = 1

// LoadPermissions reads the YAML permissions file at path.
//
// Return shape:
//   - (nil, nil) when the file does not exist — permissions are
//     optional, the caller falls back to the legacy VaultPath check.
//   - (nil, nil) when the file declares an unsupported version — a
//     slog warning is emitted so operators see the silent fall-back.
//   - (nil, err) on a real I/O or YAML parse error.
//   - (parsed, nil) on success.
func LoadPermissions(path string) (*Permissions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read permissions: %w", err)
	}

	perms, err := parsePermissionsBytes(data)
	if err != nil {
		return nil, err
	}
	if perms != nil && perms.Version > supportedPermissionsVersion {
		slog.Warn("unknown permissions version — ignoring file",
			"path", path,
			"version", perms.Version,
			"supported", supportedPermissionsVersion,
		)
		return nil, nil
	}
	return perms, nil
}

// parsePermissionsBytes unmarshals a permissions.yaml payload.
//
// It does NOT enforce the supported-version check: callers may want
// to distinguish "version too high → fall back to legacy guard" from
// "version too high → parse error" depending on whether the source is
// the operator's file (the former) or the embedded default (the
// latter must always be in-range or the build is broken).
//
// Returns:
//   - A parsed *Permissions on success.
//   - A wrapped YAML parse error on malformed input.
//
// Side effects:
//   - None.
func parsePermissionsBytes(data []byte) (*Permissions, error) {
	var perms Permissions
	if err := yaml.Unmarshal(data, &perms); err != nil {
		return nil, fmt.Errorf("parse permissions: %w", err)
	}
	return &perms, nil
}

// Match consults the tool's rules and returns a decision for path.
//
// Return shape:
//   - ("deny",  true)  when any deny glob matches.
//   - ("allow", true)  when any allow glob matches and no deny matches.
//   - ("",      false) when the tool has no entry OR no glob matches —
//     the caller falls through to the legacy VaultPath / pathguard
//     check.
//
// Deny always beats allow: both lists are evaluated independently and
// the deny verdict short-circuits regardless of allow matches.
func (p *Permissions) Match(tool, path string) (string, bool) {
	if p == nil {
		return "", false
	}
	rules, ok := p.Tools[tool]
	if !ok {
		return "", false
	}

	denied := matchesAny(rules.Deny, path)
	allowed := matchesAny(rules.Allow, path)

	switch {
	case denied:
		return "deny", true
	case allowed:
		return "allow", true
	default:
		return "", false
	}
}

// matchesAny returns true when path matches any of the doublestar globs.
// A malformed glob is treated as a non-match — we log a warning but
// never fail closed on a syntax error in operator config, mirroring the
// "permissions are advisory, legacy guard is the floor" stance of
// Slice A.
func matchesAny(globs []string, path string) bool {
	for _, g := range globs {
		ok, err := doublestar.PathMatch(g, path)
		if err != nil {
			slog.Warn("invalid permissions glob — skipping",
				"glob", g,
				"error", err,
			)
			continue
		}
		if ok {
			return true
		}
	}
	return false
}
