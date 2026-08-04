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

type queuedPrompt = QueuedPrompt

type SessionQueue struct {
	mu       sync.Mutex
	prompts  []queuedPrompt
	closed   bool
	draining bool
}

type sessionQueue = SessionQueue

func newSessionQueue() *sessionQueue {
	return &SessionQueue{}
}

func NewSessionQueue() *SessionQueue {
	return &SessionQueue{}
}

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

func (q *SessionQueue) enqueue(prompt queuedPrompt) (int, error) {
	return q.Enqueue(prompt)
}

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

func (q *SessionQueue) dequeue() *queuedPrompt {
	return q.Dequeue()
}

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

func (q *SessionQueue) cancel(promptID string) bool {
	return q.Cancel(promptID)
}

func (q *SessionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.prompts)
}

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

func (q *SessionQueue) close() {
	q.Close()
}

func (q *SessionQueue) BeginDrain() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.draining || len(q.prompts) == 0 {
		return false
	}
	q.draining = true
	return true
}

func (q *SessionQueue) beginDrain() bool {
	return q.BeginDrain()
}

func (q *SessionQueue) EndDrain() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.draining = false
}

func (q *SessionQueue) endDrain() {
	q.EndDrain()
}
