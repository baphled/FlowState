// Package all imports all builtin plugin packages so their init() functions
// register themselves with the plugin factory registry.
package all

import (
	// Import eventlogger for its init-based builtin registration.
	_ "github.com/baphled/flowstate/internal/plugin/eventlogger"
)
