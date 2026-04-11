package learning

import (
	"log/slog"
)

const defaultTriggerBufferSize = 64

// Loop is an async, opt-in observer that processes learning triggers
// from a buffered channel worker without blocking callers.
//
// It satisfies TriggerSink. Triggers are processed in the background by a
// single goroutine started by Run. When the channel is full, Notify drops
// the trigger silently and logs a warning.
type Loop struct {
	ch        chan Trigger
	store     Store
	detector  NoveltyDetector
	onFailure bool
	onNovelty bool
	done      chan struct{}
}

// LoopOption configures a Loop at creation time.
type LoopOption func(*Loop)

// WithLearningOnFailure enables automatic capture when a failure trigger arrives.
//
// Expected:
//   - enabled is true to activate failure-triggered learning.
//
// Returns:
//   - A LearningLoopOption that sets the onFailure flag.
//
// Side effects:
//   - None.
func WithLearningOnFailure(enabled bool) LoopOption {
	return func(l *Loop) {
		l.onFailure = enabled
	}
}

// WithLearningOnNovelty enables automatic capture when a novelty trigger arrives.
//
// Expected:
//   - enabled is true to activate novelty-triggered learning.
//
// Returns:
//   - A LearningLoopOption that sets the onNovelty flag.
//
// Side effects:
//   - None.
func WithLearningOnNovelty(enabled bool) LoopOption {
	return func(l *Loop) {
		l.onNovelty = enabled
	}
}

// WithNoveltyDetector sets the novelty detector used when onNovelty is enabled.
//
// Expected:
//   - d is a non-nil NoveltyDetector.
//
// Returns:
//   - A LearningLoopOption that sets the detector.
//
// Side effects:
//   - None.
func WithNoveltyDetector(d NoveltyDetector) LoopOption {
	return func(l *Loop) {
		l.detector = d
	}
}

// NewLearningLoop constructs a LearningLoop with the given store and options.
//
// Expected:
//   - store is a non-nil Store for persisting learning entries.
//   - opts configure optional behaviour (failure/novelty triggers, detector).
//
// Returns:
//   - A configured *LearningLoop ready to be started with Run.
//
// Side effects:
//   - None.
func NewLearningLoop(store Store, opts ...LoopOption) *Loop {
	l := &Loop{
		ch:    make(chan Trigger, defaultTriggerBufferSize),
		store: store,
		done:  make(chan struct{}),
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Notify enqueues a trigger for background processing.
//
// Expected:
//   - t is a valid Trigger.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Drops the trigger silently and logs a warning when the buffer is full.
func (l *Loop) Notify(t Trigger) {
	select {
	case l.ch <- t:
	default:
		slog.Warn("learning loop buffer full, dropping trigger", "agent", t.AgentID, "kind", t.Kind)
	}
}

// Run starts the background worker goroutine that processes triggers.
//
// Expected:
//   - Run is called exactly once; calling it multiple times is undefined.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Spawns a goroutine that reads from the trigger channel until Stop is called.
func (l *Loop) Run() {
	go func() {
		defer close(l.done)
		for t := range l.ch {
			l.process(t)
		}
	}()
}

// Stop closes the trigger channel, causing the background worker to drain and exit.
//
// Expected:
//   - Run has been called before Stop.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Closes the trigger channel and waits for the worker to finish.
func (l *Loop) Stop() {
	close(l.ch)
	<-l.done
}

// process handles a single trigger based on its kind and configuration.
//
// Returns: none.
// Expected: trigger kind may not match enabled conditions.
// Side effects: captures an entry to the store if conditions are met.
func (l *Loop) process(t Trigger) {
	switch t.Kind {
	case TriggerKindFailure:
		if !l.onFailure {
			return
		}
	case TriggerKindNovelty:
		if !l.onNovelty {
			return
		}
		if l.detector != nil && !l.detector.IsNovel(t.Output) {
			return
		}
	default:
		return
	}

	entry := Entry{
		AgentID:     t.AgentID,
		UserMessage: t.ID,
		Response:    t.Output,
	}
	if err := l.store.Capture(entry); err != nil {
		slog.Warn("learning loop failed to capture entry", "agent", t.AgentID, "error", err)
	}
}
