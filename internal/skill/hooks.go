package skill

// valid reports whether the event is in the closed set.
func (e HookEvent) valid() bool {
	switch e {
	case EventPacketAdmitted, EventPacketRejected, EventGateFailed, EventGatePassed:
		return true
	}
	return false
}

// NewRunner builds a hook runner with the real executor.
func NewRunner(hooks ...Hook) *Runner {
	return &Runner{hooks: hooks, exec: realExec}
}

// Bind registers one more hook binding.
func (r *Runner) Bind(h Hook) { r.hooks = append(r.hooks, h) }

// Fire reports one event; every matching hook's command runs and
// failures are collected. Unknown events are an error.
func (r *Runner) Fire(event HookEvent, subject string) []error {
	if !event.valid() {
		return []error{errInvalidEvent(event)}
	}
	var errs []error
	for _, h := range r.hooks {
		if h.Event != event {
			continue
		}
		if h.Subject != "" && h.Subject != subject {
			continue
		}
		if err := r.exec(h.Name, h.Args...); err != nil {
			errs = append(errs, err)
		}
		r.fired = append(r.fired, h)
	}
	return errs
}

// Fired lists the hooks that have run so far.
func (r *Runner) Fired() []Hook { return r.fired }
