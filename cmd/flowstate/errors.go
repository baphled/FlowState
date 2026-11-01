package main

// Error renders the cliError message, satisfying the error contract.
func (e *cliError) Error() string { return e.Message }

// Error renders the usage sentinel message.
func (usageSentinel) Error() string { return "usage" }

// Is reports equality with the session package usage sentinel.
func (usageSentinel) Is(target error) bool { return target.Error() == "usage" }
