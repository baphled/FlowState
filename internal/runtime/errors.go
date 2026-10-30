package runtime

import "fmt"

// ErrProvider marks terminal provider failures surfaced by the turn
// loop (code provider_error, exit 3 per CLI-AND-API-CONTRACT §1).
var ErrProvider = fmt.Errorf("provider_error")
