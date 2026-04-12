package engine

import (
	"context"
	"log/slog"
	"sync"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// SessionMessageSender abstracts the session manager operations needed by
// the CompletionOrchestrator. Satisfied by session.Manager.
type SessionMessageSender interface {
	// SendMessage sends a message to the given session and returns a stream of response chunks.
	SendMessage(ctx context.Context, sessionID string, message string) (<-chan provider.StreamChunk, error)
	// GetNotifications retrieves and clears pending completion notifications for a session.
	GetNotifications(sessionID string) ([]streaming.CompletionNotificationEvent, error)
	// EnsureSession creates the session if it does not already exist.
	EnsureSession(sessionID, agentID string)
}

// SessionBrokerPublisher abstracts the session broker's publish operation.
// The api.SessionBroker satisfies this interface.
type SessionBrokerPublisher interface {
	// Publish fans out stream chunks to all subscribers for the given session.
	Publish(sessionID string, chunks <-chan provider.StreamChunk)
}

// completionEvent is the internal message sent from the EventBus handler to
// the drain goroutine. It carries only the session and task identifiers.
type completionEvent struct {
	sessionID string
	taskID    string
}

// CompletionOrchestrator listens for background task completion events and
// triggers re-prompt streams when all tasks for a session have finished.
// It sits above session.Manager.SendMessage and makes re-prompting available
// to all consumers (TUI, CLI, API) via the session broker.
//
// Design:
//   - EventBus handler sends to a buffered channel non-blockingly (never blocks
//     the EventBus publish call or the background task goroutine's semaphore).
//   - A dedicated goroutine drains the channel and runs the decision logic.
//   - Per-session CAS flag prevents duplicate re-prompts from concurrent completions.
//   - Depth limit (default 3) prevents infinite re-prompt loops.
type CompletionOrchestrator struct {
	backgroundMgr *BackgroundTaskManager
	sessionMgr    SessionMessageSender
	eventBus      *eventbus.EventBus
	broker        SessionBrokerPublisher

	completionCh chan completionEvent
	stopCh       chan struct{}
	wg           sync.WaitGroup

	mu            sync.Mutex
	rePrompting   map[string]bool
	rePromptCount map[string]int
	maxRePrompts  int

	// rePromptSubs maps session IDs to channels that receive re-prompt stream
	// channels. When a subscriber exists for a session, the re-prompt stream is
	// delivered to the subscriber instead of the broker.
	subsMu       sync.RWMutex
	rePromptSubs map[string]chan<- (<-chan provider.StreamChunk)
}

// NewCompletionOrchestrator creates a new orchestrator. Call Start() to begin
// listening for events.
//
// Expected:
//   - backgroundMgr is a non-nil BackgroundTaskManager.
//   - sessionMgr is a non-nil SessionMessageSender (typically session.Manager).
//   - eventBus is a non-nil EventBus for subscribing to completion events.
//   - broker may be nil; when nil, re-prompt streams are consumed but not published.
//
// Returns:
//   - A configured but not yet started CompletionOrchestrator.
//
// Side effects:
//   - None until Start() is called.
func NewCompletionOrchestrator(
	backgroundMgr *BackgroundTaskManager,
	sessionMgr SessionMessageSender,
	eventBus *eventbus.EventBus,
	broker SessionBrokerPublisher,
) *CompletionOrchestrator {
	return &CompletionOrchestrator{
		backgroundMgr: backgroundMgr,
		sessionMgr:    sessionMgr,
		eventBus:      eventBus,
		broker:        broker,
		completionCh:  make(chan completionEvent, 64),
		stopCh:        make(chan struct{}),
		rePrompting:   make(map[string]bool),
		rePromptCount: make(map[string]int),
		maxRePrompts:  3,
		rePromptSubs:  make(map[string]chan<- (<-chan provider.StreamChunk)),
	}
}

// Start subscribes to background task completion and failure events on the
// EventBus and launches the drain goroutine.
//
// Side effects:
//   - Subscribes two handlers to the EventBus.
//   - Spawns one goroutine that runs until Stop() is called.
func (o *CompletionOrchestrator) Start() {
	o.eventBus.Subscribe(events.EventBackgroundTaskCompleted, o.handleEvent)
	o.eventBus.Subscribe(events.EventBackgroundTaskFailed, o.handleEvent)

	o.wg.Add(1)
	go o.drainLoop()
}

// Stop signals the drain goroutine to exit and waits for it to finish.
//
// Side effects:
//   - Closes the stop channel, causing the drain goroutine to exit.
//   - Unsubscribes from EventBus events.
func (o *CompletionOrchestrator) Stop() {
	close(o.stopCh)
	o.wg.Wait()

	o.eventBus.Unsubscribe(events.EventBackgroundTaskCompleted, o.handleEvent)
	o.eventBus.Unsubscribe(events.EventBackgroundTaskFailed, o.handleEvent)
}

// handleEvent is the EventBus handler called synchronously during Publish.
// It extracts the session ID from the event and sends a completionEvent to
// the buffered channel non-blockingly. If the channel is full, it logs a
// warning and drops the event (the system is self-correcting: the next
// completion will re-check all pending notifications).
//
// Expected:
//   - event is a *BackgroundTaskCompletedEvent or *BackgroundTaskFailedEvent.
//
// Side effects:
//   - Sends to completionCh or logs a warning on channel full.
func (o *CompletionOrchestrator) handleEvent(event any) {
	var sessionID, taskID string

	switch e := event.(type) {
	case *events.BackgroundTaskCompletedEvent:
		sessionID = e.Data.SessionID
		taskID = e.Data.TaskID
	case *events.BackgroundTaskFailedEvent:
		sessionID = e.Data.SessionID
		taskID = e.Data.TaskID
	default:
		return
	}

	if sessionID == "" {
		return
	}

	select {
	case o.completionCh <- completionEvent{sessionID: sessionID, taskID: taskID}:
	default:
		slog.Warn("completion orchestrator: channel full, dropping event",
			"session_id", sessionID, "task_id", taskID)
	}
}

// drainLoop is the dedicated goroutine that processes completion events.
// It exits when stopCh is closed.
//
// Side effects:
//   - Calls processCompletion for each event received on completionCh.
func (o *CompletionOrchestrator) drainLoop() {
	defer o.wg.Done()

	for {
		select {
		case <-o.stopCh:
			return
		case evt := <-o.completionCh:
			o.processCompletion(evt)
		}
	}
}

// processCompletion handles a single completion event. It checks whether all
// tasks for the session are done, acquires the CAS flag, and triggers a
// re-prompt if conditions are met.
//
// Expected:
//   - evt contains a valid sessionID.
//
// Side effects:
//   - May trigger a re-prompt stream via triggerRePrompt.
func (o *CompletionOrchestrator) processCompletion(evt completionEvent) {
	if o.backgroundMgr.ActiveCountForSession(evt.sessionID) > 0 {
		return
	}

	// CAS: only one re-prompt per session at a time.
	o.mu.Lock()
	if o.rePrompting[evt.sessionID] {
		o.mu.Unlock()
		return
	}
	if o.rePromptCount[evt.sessionID] >= o.maxRePrompts {
		o.mu.Unlock()
		slog.Warn("completion orchestrator: re-prompt depth limit reached",
			"session_id", evt.sessionID, "max", o.maxRePrompts)
		return
	}
	o.rePrompting[evt.sessionID] = true
	o.mu.Unlock()

	o.triggerRePrompt(evt.sessionID)
}

// triggerRePrompt retrieves pending notifications, formats them, sends a
// message to the session manager, and publishes the result to the broker.
//
// Expected:
//   - sessionID identifies an active session with pending notifications.
//
// Side effects:
//   - Calls SendMessage on the session manager.
//   - Publishes the resulting stream to the broker (if configured).
//   - Increments the re-prompt counter and clears the CAS flag.
func (o *CompletionOrchestrator) triggerRePrompt(sessionID string) {
	defer func() {
		o.mu.Lock()
		o.rePromptCount[sessionID]++
		o.rePrompting[sessionID] = false
		o.mu.Unlock()
	}()

	notifications, err := o.sessionMgr.GetNotifications(sessionID)
	if err != nil || len(notifications) == 0 {
		return
	}

	reminder := FormatCompletionReminders(notifications)
	if reminder == "" {
		return
	}

	ctx := context.Background()
	chunks, err := o.sessionMgr.SendMessage(ctx, sessionID, reminder)
	if err != nil {
		slog.Warn("completion orchestrator: re-prompt SendMessage failed",
			"session_id", sessionID, "error", err)
		return
	}

	// If a direct subscriber exists (e.g. TUI), deliver the stream to it
	// instead of the broker. This avoids duplicate chunk delivery.
	o.subsMu.RLock()
	sub, hasSub := o.rePromptSubs[sessionID]
	o.subsMu.RUnlock()

	if hasSub {
		select {
		case sub <- chunks:
		default:
			slog.Warn("completion orchestrator: re-prompt subscriber full, falling back to broker",
				"session_id", sessionID)
			o.publishOrDrain(sessionID, chunks)
		}
		return
	}

	o.publishOrDrain(sessionID, chunks)
}

// publishOrDrain publishes chunks to the broker if configured, otherwise
// drains the channel to ensure the stream completes.
//
// Expected:
//   - chunks is a non-nil channel from SendMessage.
//
// Side effects:
//   - Publishes to broker or drains the channel.
func (o *CompletionOrchestrator) publishOrDrain(sessionID string, chunks <-chan provider.StreamChunk) {
	if o.broker != nil {
		o.broker.Publish(sessionID, chunks)
	} else {
		for range chunks { //nolint:revive // intentional drain
		}
	}
}

// ResetRePromptCount clears the re-prompt depth counter for a session,
// typically called when the user sends a new message (resetting the
// autonomous re-prompt budget).
//
// Expected:
//   - sessionID is a valid session identifier.
//
// Side effects:
//   - Resets the re-prompt counter for the given session.
func (o *CompletionOrchestrator) ResetRePromptCount(sessionID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.rePromptCount, sessionID)
}

// SubscribeRePrompt registers a channel to receive re-prompt stream channels
// for the given session. When a re-prompt is triggered, the resulting stream
// is sent on the returned channel instead of being published to the session
// broker, allowing the TUI to read chunks incrementally.
//
// Expected:
//   - sessionID is a valid session identifier.
//
// Returns:
//   - A receive-only channel that delivers re-prompt stream channels.
//
// Side effects:
//   - Registers the subscription; call UnsubscribeRePrompt to clean up.
func (o *CompletionOrchestrator) SubscribeRePrompt(sessionID string) <-chan (<-chan provider.StreamChunk) {
	ch := make(chan (<-chan provider.StreamChunk), 1)

	o.subsMu.Lock()
	o.rePromptSubs[sessionID] = ch
	o.subsMu.Unlock()

	return ch
}

// UnsubscribeRePrompt removes the re-prompt subscription for the given
// session and closes the subscriber channel.
//
// Expected:
//   - sessionID was previously passed to SubscribeRePrompt.
//
// Side effects:
//   - Closes the subscriber channel and removes it from the map.
func (o *CompletionOrchestrator) UnsubscribeRePrompt(sessionID string) {
	o.subsMu.Lock()
	if ch, ok := o.rePromptSubs[sessionID]; ok {
		close(ch)
		delete(o.rePromptSubs, sessionID)
	}
	o.subsMu.Unlock()
}
