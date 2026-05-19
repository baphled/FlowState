package turn

import "github.com/google/uuid"

// defaultIDGen mints a turn_id using google/uuid.NewString. Pulled
// out as a package-level function so NewRegistryWithIDGen's nil-
// fallback can reference it without leaking the import into the
// public surface. Production callers go through NewRegistry which
// passes this function in.
func defaultIDGen() string {
	return uuid.NewString()
}
