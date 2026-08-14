package dispatch

import (
	"context"
	"errors"
	"sync"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/google/uuid"
)

// ErrQueueOverflow reports that a session queue reached its depth limit.
var ErrQueueOverflow = errors.New("dispatch: queued prompt overflow")

// MaxQueueDepth is the maximum number of prompts a session queue accepts.
const MaxQueueDepth = 64

const maxQueueDepth = MaxQueueDepth

var errQueueClosed = errors.New("dispatch: session queue closed")

// QueuedPrompt is the minimal prompt payload stored in a session queue.
type QueuedPrompt struct {
	PromptID  string
	SessionID string
	Request   DispatchRequest
	Ctx       context.Context
	Consumer  streaming.StreamConsumer
}

// queuedPrompt is an alias for QueuedPrompt used internally by the session queue.
type queuedPrompt = QueuedPrompt

// SessionQueue is a concurrency-safe FIFO queue of dispatch prompts for a single session.
type SessionQueue struct {
	mu       sync.Mutex
	prompts  []queuedPrompt
	closed   bool
	draining bool
}

// sessionQueue is an alias for SessionQueue used by internal constructors.
type sessionQueue = SessionQueue

// newSessionQueue ...
//
// Returns: result of newSessionQueue.
//
// Side effects: None.
func newSessionQueue() *sessionQueue {
	return &SessionQueue{}
}

// NewSessionQueue ...
//
// Returns: result of NewSessionQueue.
//
// Side effects: None.
func NewSessionQueue() *SessionQueue {
	return &SessionQueue{}
}

// Enqueue ...
//
// Expected: parameters for Enqueue.
//
// Returns: result of Enqueue.
//
// Side effects: None.
func (q *SessionQueue) Enqueue(prompt queuedPrompt) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0, errQueueClosed
	}
	if len(q.prompts) >= maxQueueDepth {
		return 0, ErrQueueOverflow
	}
	if prompt.PromptID == "" {
		prompt.PromptID = uuid.NewString()
	}
	q.prompts = append(q.prompts, prompt)
	return len(q.prompts), nil
}

// enqueue ...
//
// Expected: parameters for enqueue.
//
// Returns: result of enqueue.
//
// Side effects: None.
func (q *SessionQueue) enqueue(prompt queuedPrompt) (int, error) {
	return q.Enqueue(prompt)
}

// Dequeue ...
//
// Expected: parameters for Dequeue.
//
// Returns: result of Dequeue.
//
// Side effects: None.
func (q *SessionQueue) Dequeue() *queuedPrompt {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.prompts) == 0 {
		return nil
	}
	prompt := q.prompts[0]
	q.prompts[0] = queuedPrompt{}
	q.prompts = q.prompts[1:]
	return &prompt
}

// dequeue ...
//
// Expected: parameters for dequeue.
//
// Returns: result of dequeue.
//
// Side effects: None.
func (q *SessionQueue) dequeue() *queuedPrompt {
	return q.Dequeue()
}

// Cancel ...
//
// Expected: parameters for Cancel.
//
// Returns: result of Cancel.
//
// Side effects: None.
func (q *SessionQueue) Cancel(promptID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.prompts {
		if q.prompts[i].PromptID != promptID {
			continue
		}
		copy(q.prompts[i:], q.prompts[i+1:])
		q.prompts[len(q.prompts)-1] = queuedPrompt{}
		q.prompts = q.prompts[:len(q.prompts)-1]
		return true
	}
	return false
}

// cancel ...
//
// Expected: parameters for cancel.
//
// Returns: result of cancel.
//
// Side effects: None.
func (q *SessionQueue) cancel(promptID string) bool {
	return q.Cancel(promptID)
}

// Len ...
//
// Expected: parameters for Len.
//
// Returns: result of Len.
//
// Side effects: None.
func (q *SessionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.prompts)
}

// Close ...
//
// Expected: parameters for Close.
//
// Returns: result of Close.
//
// Side effects: None.
func (q *SessionQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.draining = false
	for i := range q.prompts {
		q.prompts[i] = queuedPrompt{}
	}
	q.prompts = nil
}

// close ...
//
// Expected: parameters for close.
//
// Returns: result of close.
//
// Side effects: None.
func (q *SessionQueue) close() {
	q.Close()
}

// BeginDrain ...
//
// Expected: parameters for BeginDrain.
//
// Returns: result of BeginDrain.
//
// Side effects: None.
func (q *SessionQueue) BeginDrain() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.draining || len(q.prompts) == 0 {
		return false
	}
	q.draining = true
	return true
}

// beginDrain ...
//
// Expected: parameters for beginDrain.
//
// Returns: result of beginDrain.
//
// Side effects: None.
func (q *SessionQueue) beginDrain() bool {
	return q.BeginDrain()
}

// EndDrain ...
//
// Expected: parameters for EndDrain.
//
// Returns: result of EndDrain.
//
// Side effects: None.
func (q *SessionQueue) EndDrain() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.draining = false
}

// endDrain ...
//
// Expected: parameters for endDrain.
//
// Returns: result of endDrain.
//
// Side effects: None.
func (q *SessionQueue) endDrain() {
	q.EndDrain()
}
