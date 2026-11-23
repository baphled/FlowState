package app

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/baphled/flowstate/internal/store"
)

// storeDriverModule is the SQLite driver the store links.
const storeDriverModule = "modernc.org/sqlite"

// ReadStoreInfo describes the database at path without changing it:
// the store opens read-only, so nothing is migrated, rebuilt or backed
// up, the database file is never modified and an old database reports
// the version it holds. SQLite may create or update the -wal and -shm
// sidecar files of a WAL-mode database. An empty path or a missing file
// is a usage error (exit 2); a file that cannot be read as a database is
// config_invalid (exit 4).
func ReadStoreInfo(path string) (StoreInfo, error) {
	if path == "" {
		return StoreInfo{}, fmt.Errorf("%w: no database path given", ErrUsage)
	}
	st, err := store.OpenReadOnly(path)
	if errors.Is(err, store.ErrNoDatabase) {
		return StoreInfo{}, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if err != nil {
		return StoreInfo{}, fmt.Errorf("%w: cannot open database %q: %w", ErrConfigInvalid, path, err)
	}
	defer func() { _ = st.Close() }()
	facts, err := st.Info()
	if err != nil {
		return StoreInfo{}, fmt.Errorf("%w: cannot read database %q: %w", ErrConfigInvalid, path, err)
	}
	info := StoreInfo{
		SchemaVersion:       facts.SchemaVersion,
		JournalMode:         facts.JournalMode,
		ForeignKeysEnforced: facts.ForeignKeysEnforced,
		Driver:              storeDriverModule,
		DriverVersion:       moduleVersion(storeDriverModule),
		SQLiteVersion:       facts.SQLiteVersion,
		Path:                path,
		GoVersion:           runtime.Version(),
		Tables:              make([]StoreTableInfo, 0, len(facts.Tables)),
	}
	for _, t := range facts.Tables {
		info.Tables = append(info.Tables, StoreTableInfo{Name: t.Name, ForeignKeys: t.ForeignKeys})
	}
	return info, nil
}

// moduleVersion returns the version of module in the running binary's
// build info, or "unknown" when there is none (a test binary carries no
// dependency list on some toolchains).
func moduleVersion(module string) string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return versionIn(bi, module)
}

// versionIn finds module among the dependencies of bi.
func versionIn(bi *debug.BuildInfo, module string) string {
	for _, dep := range bi.Deps {
		if dep.Path == module && dep.Version != "" {
			return dep.Version
		}
	}
	return "unknown"
}
