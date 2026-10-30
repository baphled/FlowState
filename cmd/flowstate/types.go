package main

// cliError is a failure carrying one of the closed error codes; it
// renders as a single JSON object on stderr.
type cliError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// flags holds the parsed global flags for one invocation.
type flags struct {
	db     string
	json   bool
	status string
	// session new
	provider string
	model    string
	task     string
	// session tool
	tool      string
	toolArg   string
	allow     []string
	allowFile string
	// session run
	budget int64
}

// usageSentinel satisfies errors.Is against the session package
// usage sentinel while formatting a usage message.
type usageSentinel struct{}
