package main

// Error renders the cliError message, satisfying the error contract.
func (e *cliError) Error() string { return e.Message }

// Error renders the usage sentinel message.
func (usageSentinel) Error() string { return "usage" }
