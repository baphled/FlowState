package swarm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// schemaFileExtension is the on-disk extension the directory loader
// scans for. JSON Schema documents are conventionally `.json`; YAML is
// not supported in Phase 2 because the canonical jsonschema-go decoder
// does not round-trip YAML losslessly and the CLI tooling around the
// schemas (drafting, validation in editors) overwhelmingly assumes
// JSON. Add `.yaml` later behind a separate decoder if a real demand
// surfaces.
const schemaFileExtension = ".json"

// SchemaDirLoader walks dir and registers every `*.json` file under
// its basename (filename minus `.json`). The loader is a deliberate
// no-op when dir is empty or absent so unconfigured operators still
// boot — schemas referenced by manifests then surface a "not
// registered" GateError at firing time, which is more diagnostic than
// failing app startup.
//
// Precedence rationale: file-based registrations OVERRIDE programmatic
// seeds. Both surfaces compose at startup — call SeedDefaultSchemas
// FIRST to install the bundled `review-verdict-v1`, then call
// SchemaDirLoader to walk the operator's drop-in directory. Operators
// who write `${ConfigDir}/schemas/review-verdict-v1.json` are making an
// explicit, auditable choice to override the bundled shape; the file
// path itself is the audit trail. Going the other direction (built-in
// always wins) would silently ignore the operator's drop-in and force
// them to debug "why isn't my schema overriding". Neither direction is
// "wrong" — the file-wins choice is documented here so the precedence
// is auditable in code review without re-reading the spec.
//
// Errors loading individual schemas log a WARN via slog and the loader
// continues with the rest of the directory. The aggregated count of
// registered / failed entries is returned so the caller can surface a
// summary log at startup.
type SchemaDirLoader struct {
	// dir is the absolute path to the schemas directory. Stored
	// verbatim so log lines naming it are auditable; expansion of
	// `~` / env vars is the caller's job (see config.expandPaths).
	dir string

	// fs is the filesystem the loader reads from. Production wiring
	// passes os.DirFS(dir); tests inject an in-memory fstest.MapFS so
	// the loader has no I/O side effects. Nil falls back to
	// os.DirFS(dir) at Load time so callers that only set dir still
	// work.
	fs fs.FS
}

// NewSchemaDirLoader returns a loader rooted at dir. The loader does
// not read the filesystem until Load is called.
//
// Expected:
//   - dir is the absolute path to the schemas directory. Empty is
//     allowed — Load returns a no-op summary in that case.
//
// Returns:
//   - A configured *SchemaDirLoader.
//
// Side effects:
//   - None.
func NewSchemaDirLoader(dir string) *SchemaDirLoader {
	return &SchemaDirLoader{dir: dir}
}

// WithFS overrides the filesystem the loader reads from. Used by tests
// to inject a deterministic in-memory tree without touching the
// operator's real filesystem.
//
// Expected:
//   - fsys is a non-nil fs.FS rooted at the same logical "dir" as the
//     loader was constructed with. Production callers leave the
//     default os.DirFS in place by NOT calling this method.
//
// Returns:
//   - The loader for chaining.
//
// Side effects:
//   - None.
func (l *SchemaDirLoader) WithFS(fsys fs.FS) *SchemaDirLoader {
	l.fs = fsys
	return l
}

// SchemaLoadSummary is the return shape from SchemaDirLoader.Load. The
// caller logs the counts at INFO so operators see how many drop-in
// schemas were picked up, and inspects Failed for a count of skipped
// entries.
type SchemaLoadSummary struct {
	// Dir is the directory that was scanned. Empty when the loader
	// was constructed with an empty dir.
	Dir string

	// Registered counts the schemas registered successfully (i.e.
	// RegisterSchema returned nil for them).
	Registered int

	// Failed counts files that were either malformed JSON, failed
	// schema-resolution, or otherwise could not be registered. Each
	// failure already logged a WARN so the count alone is enough for
	// the caller's startup summary.
	Failed int

	// Names lists the schema names registered, sorted by directory-
	// walk order. Useful for tests that pin "exactly these schemas
	// were picked up".
	Names []string
}

// Load walks the configured directory and registers every `*.json`
// file under its basename. The returned summary is populated whether
// or not the directory existed.
//
// Behaviour matrix:
//   - dir == ""           — returns an empty summary, no I/O.
//   - dir absent          — returns an empty summary, no error, INFO log.
//   - dir empty           — returns an empty summary, no schemas registered.
//   - dir has *.json      — each file is parsed; valid files register,
//                           failures log WARN and are counted in Failed.
//   - dir has subdirs     — recursion is intentionally NOT followed.
//                           Operators flatten their schema tree under
//                           dir; nested layouts are deferred to a
//                           future "schema bundles" feature so the
//                           Phase 2 contract stays a one-level tree
//                           that mirrors agents/ and skills/.
//
// Expected:
//   - The package-level schema registry has already been seeded by
//     any callers that want programmatic registrations to come first.
//     Any drop-in file matching a seeded name will overwrite it (file
//     wins; see SchemaDirLoader doc for rationale).
//
// Returns:
//   - A populated SchemaLoadSummary. The error return is reserved for
//     truly catastrophic failures (a non-IsNotExist filesystem error
//     reading the directory itself); per-file failures log WARN and
//     are counted in Failed rather than aborting the whole walk.
//
// Side effects:
//   - Calls RegisterSchema for every registered schema, taking the
//     registry's write lock once per file.
//   - Emits structured slog WARN / INFO records for the directory
//     state and per-file failures.
func (l *SchemaDirLoader) Load() (SchemaLoadSummary, error) {
	summary := SchemaLoadSummary{Dir: l.dir, Names: []string{}}
	if l.dir == "" {
		return summary, nil
	}

	fsys := l.fs
	if fsys == nil {
		if _, err := os.Stat(l.dir); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				slog.Info("swarm schema dir absent", "dir", l.dir)
				return summary, nil
			}
			return summary, fmt.Errorf("stat %q: %w", l.dir, err)
		}
		fsys = os.DirFS(l.dir)
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			slog.Info("swarm schema dir absent", "dir", l.dir)
			return summary, nil
		}
		return summary, fmt.Errorf("read %q: %w", l.dir, err)
	}

	for _, entry := range entries {
		l.registerEntry(fsys, entry, &summary)
	}
	return summary, nil
}

// registerEntry handles one directory entry: skips non-files and
// non-`.json` files, parses the JSON, and calls RegisterSchema. Pulled
// into a helper so Load's body stays focused on the directory walk.
//
// Expected:
//   - fsys is the filesystem rooted at l.dir.
//   - entry is one fs.DirEntry from fs.ReadDir.
//   - summary is the running tally; mutated in place.
//
// Returns:
//   - None. Failures log WARN and increment summary.Failed.
//
// Side effects:
//   - Reads the file contents.
//   - Calls RegisterSchema on success.
//   - Logs WARN / DEBUG via slog on per-file outcomes.
func (l *SchemaDirLoader) registerEntry(fsys fs.FS, entry fs.DirEntry, summary *SchemaLoadSummary) {
	if entry.IsDir() {
		return
	}
	name := entry.Name()
	if !strings.HasSuffix(strings.ToLower(name), schemaFileExtension) {
		return
	}
	schemaName := strings.TrimSuffix(name, schemaFileExtension)
	payload, err := fs.ReadFile(fsys, name)
	if err != nil {
		slog.Warn("read swarm schema file", "dir", l.dir, "file", name, "error", err)
		summary.Failed++
		return
	}
	schema, err := decodeSchemaPayload(payload)
	if err != nil {
		slog.Warn("decode swarm schema file", "dir", l.dir, "file", name, "error", err)
		summary.Failed++
		return
	}
	if err := RegisterSchema(schemaName, schema); err != nil {
		slog.Warn("register swarm schema", "dir", l.dir, "file", name, "name", schemaName, "error", err)
		summary.Failed++
		return
	}
	summary.Registered++
	summary.Names = append(summary.Names, schemaName)
}

// decodeSchemaPayload parses a JSON Schema document into the
// jsonschema-go shape. Wrapped so the per-file error path stays
// identifiable in logs without the caller dragging json/encoding
// errors through the loader's interface.
//
// Expected:
//   - payload is the raw bytes of a JSON Schema file.
//
// Returns:
//   - A non-nil *jsonschema.Schema and nil on success.
//   - nil and a wrapped error when payload is not a valid JSON
//     Schema.
//
// Side effects:
//   - None.
func decodeSchemaPayload(payload []byte) (*jsonschema.Schema, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(payload, &schema); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return &schema, nil
}

// LoadSchemasFromDir is a convenience wrapper that constructs a loader
// rooted at dir and runs it. Used at app-startup so the call site does
// not need to import fs or wrangle the loader struct directly.
//
// Expected:
//   - dir is the absolute path to the schemas directory.
//
// Returns:
//   - A populated SchemaLoadSummary and nil on success.
//   - An empty summary and an error on a catastrophic stat/read
//     failure.
//
// Side effects:
//   - See SchemaDirLoader.Load.
func LoadSchemasFromDir(dir string) (SchemaLoadSummary, error) {
	return NewSchemaDirLoader(dir).Load()
}

// ResolveSchemaDir returns the effective schemas directory for a
// given override. An empty override yields the default
// `${ConfigDir}/schemas` path; the caller is responsible for
// expanding any tilde / env-var references before calling.
//
// Helper kept here (rather than in config) so schema-loading
// callsites stay decoupled from the AppConfig type — tests for the
// loader can compute the default without importing config.
//
// Expected:
//   - configDir is the resolved XDG_CONFIG_HOME flowstate root.
//   - override is an optional explicit path; empty means "use default".
//
// Returns:
//   - The absolute path to the schemas directory.
//
// Side effects:
//   - None.
func ResolveSchemaDir(configDir, override string) string {
	if override != "" {
		return override
	}
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "schemas")
}
