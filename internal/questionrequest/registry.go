package questionrequest

import (
	"context"
	"errors"
	"sync"
	"time"
)

// QuestionRequest captures the metadata published when the question
// tool suspends awaiting the operator's answer. RequestID is unique
// across the running daemon — collisions return
// ErrQuestionRequestExists at Register time.
type QuestionRequest struct {
	RequestID     string
	ToolName      string
	AgentName     string
	Question      string
	Options       []string
	AllowMultiple bool
	SessionID     string
	ChainID       string
	CreatedAt     time.Time
}

// QuestionAnswer is the operator's resolution of a suspended question.
// Answers carries the selected option strings (or free text when the
// question carried no options).
type QuestionAnswer struct {
	RequestID string
	Answers   []string
}

// ErrQuestionRequestExists is returned by Register when the supplied
// RequestID is already present. Sentinel so callers can errors.Is-match
// it.
var ErrQuestionRequestExists = errors.New("questionrequest: duplicate request_id")

// ErrQuestionRequestNotFound is returned by Resolve and Wait when the
// supplied RequestID is unknown. Distinct sentinel so a late answer
// from a stale tab can be distinguished from a duplicate register.
var ErrQuestionRequestNotFound = errors.New("questionrequest: request not found")

// pendingQuestion carries the persisted QuestionRequest alongside the
// buffered channel Wait blocks on. The channel capacity of 1 means a
// Resolve arriving before Wait does not deadlock.
type pendingQuestion struct {
	req      QuestionRequest
	answerCh chan QuestionAnswer
}

// Registry is the in-process store of suspended questions. Construct
// via NewRegistry; the zero value is NOT usable.
type Registry struct {
	mu              sync.Mutex
	byID            map[string]*pendingQuestion
	byAnswered      map[string]QuestionAnswer
	byActiveSession map[string][]string
}

// NewRegistry constructs a Registry with the internal maps initialised.
//
// Returns:
//   - A ready-to-use Registry with no pending questions.
//
// Side effects:
//   - None.
func NewRegistry() *Registry {
	return &Registry{
		byID:            make(map[string]*pendingQuestion),
		byAnswered:      make(map[string]QuestionAnswer),
		byActiveSession: make(map[string][]string),
	}
}

// Register inserts a fresh question request into the registry.
// Returns ErrQuestionRequestExists when req.RequestID is already
// present.
//
// Expected:
//   - req.RequestID is non-empty and not already registered.
//
// Returns:
//   - ErrQuestionRequestExists on a duplicate RequestID, nil otherwise.
//
// Side effects:
//   - Inserts the request and appends its ID to the session's pending list.
func (r *Registry) Register(req QuestionRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[req.RequestID]; exists {
		return ErrQuestionRequestExists
	}
	r.byID[req.RequestID] = &pendingQuestion{
		req:      req,
		answerCh: make(chan QuestionAnswer, 1),
	}
	r.byActiveSession[req.SessionID] = append(r.byActiveSession[req.SessionID], req.RequestID)
	return nil
}

// Resolve delivers the operator's answer to the suspended Wait caller
// by sending into the buffered answer channel and removing the pending
// entry. Returns ErrQuestionRequestNotFound when requestID is unknown.
//
// Expected:
//   - requestID identifies a currently pending question.
//
// Returns:
//   - ErrQuestionRequestNotFound when the request is unknown or already
//     resolved; nil otherwise.
//
// Side effects:
//   - Delivers the answer to the suspended Wait caller and moves the
//     request to the answered set.
func (r *Registry) Resolve(requestID string, answer QuestionAnswer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	pending, exists := r.byID[requestID]
	if !exists {
		return ErrQuestionRequestNotFound
	}
	answer.RequestID = requestID
	r.byAnswered[requestID] = answer
	select {
	case pending.answerCh <- answer:
	default:
	}
	delete(r.byID, requestID)
	r.removeFromSessionLocked(pending.req.SessionID, requestID)
	return nil
}

// Wait blocks until an answer arrives via Resolve, or until ctx is
// cancelled. On cancellation Wait returns the zero QuestionAnswer and
// ctx.Err() so the caller can distinguish "operator answered" from
// "request timed out or was withdrawn". Returns
// ErrQuestionRequestNotFound when requestID is unknown — Register must
// precede Wait.
//
// Expected:
//   - requestID identifies a registered question; Resolve may arrive
//     before or after Wait starts (the answer channel is buffered).
//
// Returns:
//   - The operator's QuestionAnswer, or the zero value with ctx.Err()
//     when the context is cancelled.
//
// Side effects:
//   - Blocks the calling goroutine until an answer or cancellation.
func (r *Registry) Wait(ctx context.Context, requestID string) (QuestionAnswer, error) {
	r.mu.Lock()
	if answer, ok := r.byAnswered[requestID]; ok {
		delete(r.byAnswered, requestID)
		r.mu.Unlock()
		return answer, nil
	}
	pending, exists := r.byID[requestID]
	r.mu.Unlock()
	if !exists {
		return QuestionAnswer{}, ErrQuestionRequestNotFound
	}

	select {
	case answer := <-pending.answerCh:
		return answer, nil
	case <-ctx.Done():
		r.cancelPending(requestID)
		return QuestionAnswer{}, ctx.Err()
	}
}

// PendingForSession returns a snapshot of request IDs currently
// suspended for sessionID, in arrival order. Never returns nil.
//
// Expected:
//   - sessionID may be any string, including "" for engine-less callers.
//
// Returns:
//   - A copy of the pending request IDs attributed to the session.
//
// Side effects:
//   - None (read-only under lock).
func (r *Registry) PendingForSession(sessionID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	ids := r.byActiveSession[sessionID]
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

// Lookup returns the registered QuestionRequest for requestID. The
// second return value is false when the request is unknown. The
// returned struct is a value copy.
//
// Expected:
//   - requestID may be any string.
//
// Returns:
//   - The registered QuestionRequest value copy; false when unknown.
//
// Side effects:
//   - None (read-only under lock).
func (r *Registry) Lookup(requestID string) (QuestionRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.byID[requestID]
	if !ok {
		return QuestionRequest{}, false
	}
	return pending.req, true
}

// PendingCount returns the total number of in-flight question requests
// across all sessions.
//
// Returns:
//   - The number of currently pending (unresolved) questions.
//
// Side effects:
//   - None (read-only under lock).
func (r *Registry) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID)
}

// cancelPending removes a request from the registry maps. Tolerates a
// concurrent Resolve that already removed the entry.
//
// Expected:
//   - requestID may already have been resolved concurrently; that is
//     tolerated as a no-op.
//
// Side effects:
//   - Removes the request from the registry maps so later lookups fail.
func (r *Registry) cancelPending(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, exists := r.byID[requestID]
	if !exists {
		return
	}
	delete(r.byID, requestID)
	r.removeFromSessionLocked(pending.req.SessionID, requestID)
}

// removeFromSessionLocked drops requestID from
// byActiveSession[sessionID]. MUST be called with r.mu held.
//
// Expected:
//   - r.mu is already held by the caller.
//
// Side effects:
//   - Mutates the session's pending-ID slice in place.
func (r *Registry) removeFromSessionLocked(sessionID, requestID string) {
	ids := r.byActiveSession[sessionID]
	out := ids[:0]
	for _, id := range ids {
		if id == requestID {
			continue
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		delete(r.byActiveSession, sessionID)
		return
	}
	r.byActiveSession[sessionID] = out
}
