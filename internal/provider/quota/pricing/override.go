package pricing

import (
	"errors"
	"fmt"
	"os"
)

// LoadOverride reads the operator-supplied pricing table at path and
// returns a Table stamped with Source=SourceOverrideString(path).
//
// Per plan §"Pricing table" lines 342-344, the operator-override file
// is the highest precedence tier — partial files merge OVER the
// embedded baseline (the Resolver handles the merge by per-key lookup
// at Lookup time, so partial coverage is the expected case). A file
// that defines only "openai/gpt-4o" still wins for that key but every
// other model falls through to the registry / embedded tiers.
//
// Returns the zero Table and a nil error when path is empty — the
// "no override configured" case is a quiet success per the YAML
// defaults (an absent `quota.pricing.path` key omits the override
// tier entirely).
//
// Errors surface from the filesystem read (path-not-found, permission)
// and from ParseTable (malformed JSON, unsupported version, empty
// models, non-positive prices). The caller logs and falls back to
// the registry / embedded baseline rather than failing boot — the
// plan's honesty stance prefers a stale price over no price.
func LoadOverride(path string) (Table, error) {
	if path == "" {
		return Table{}, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path is intentional
	if err != nil {
		return Table{}, fmt.Errorf("pricing: reading override file %q: %w", path, err)
	}
	t, err := ParseTable(data)
	if err != nil {
		return Table{}, fmt.Errorf("pricing: parsing override file %q: %w", path, err)
	}
	t.Source = SourceOverrideString(path)
	return t, nil
}

// ErrOverrideNotConfigured is returned by helpers that want to
// distinguish "no override path set" from a parse error.
//
// LoadOverride itself returns (zero Table, nil) for empty path so the
// caller's nil-error check is the simplest branch. Callers needing
// the explicit distinction (the boot-validation path) can construct
// their own switch on path == "".
var ErrOverrideNotConfigured = errors.New("pricing: override path not configured")
