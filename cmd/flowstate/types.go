package main

type cliError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

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
	budget    int64
	maxTokens int64
}

type usageSentinel struct{}
