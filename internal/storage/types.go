package storage

// OpenInfo records the effective runtime configuration observed after opening
// a database. T019 requires these to be recorded, not assumed.
type OpenInfo struct {
	DriverName    string            `json:"driver_name"`
	DriverVersion string            `json:"driver_version"`
	GoVersion     string            `json:"go_version"`
	SQLiteVersion string            `json:"sqlite_version"`
	PragmaTimeUTC string            `json:"pragma_time_utc"`
	Pragmas       map[string]string `json:"pragmas"`
}
