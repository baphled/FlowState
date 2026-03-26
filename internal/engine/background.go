// Package engine provides the AI agent execution engine.
package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// BackgroundTask represents a tracked background execution.
type BackgroundTask struct {
	ID          string
	AgentID     string
	Description string
	Status      atomicValue
	StartedAt   time.Time
	CompletedAt *time.Time
	Result      string
	Error       error
	cancel      context.CancelFunc
}

// atomicValue provides atomic string operations.
type atomicValue struct {
	v atomic.Value
}

// Load returns the current string value.
//
// Returns:
//   - The stored string value, or an empty string when unset.
//
// Side effects:
//   - None.
func (a *atomicValue) Load() string {
	if v, ok := a.v.Load().(string); ok {
		return v
	}
	return ""
}

// store sets the string value atomically.
//
// Expected:
//   - s is the value to store.
//
// Side effects:
//   - Updates the underlying atomic value.
func (a *atomicValue) store(s string) {
	a.v.Store(s)
}

// BackgroundTaskManager tracks parallel delegation tasks.
type BackgroundTaskManager struct {
	tasks map[string]*BackgroundTask
	mu    sync.RWMutex
}

// NewBackgroundTaskManager creates a new task manager with an empty task map.
//
// Returns:
//   - A ready-to-use BackgroundTaskManager instance.
//
// Side effects:
//   - None.
func NewBackgroundTaskManager() *BackgroundTaskManager {
	return &BackgroundTaskManager{
		tasks: make(map[string]*BackgroundTask),
	}
}

// Launch starts a background task by executing the provided function asynchronously.
// The task is tracked by ID and its status is updated upon completion.
//
// Expected:
//   - ctx is a valid context for the background operation.
//   - id is a unique identifier for the task.
//   - agentID identifies the agent that owns this task.
//   - desc describes the task for tracking purposes.
//   - fn is the function to execute asynchronously.
//
// Returns:
//   - The created BackgroundTask, already marked as running.
//
// Side effects:
//   - Spawns a goroutine to execute fn.
//   - Updates task status to "completed", "failed", or "cancelled".
func (m *BackgroundTaskManager) Launch(
	ctx context.Context,
	id, agentID, desc string,
	fn func(ctx context.Context) (string, error),
) *BackgroundTask {
	taskCtx, cancel := context.WithCancel(ctx)

	task := &BackgroundTask{
		ID:          id,
		AgentID:     agentID,
		Description: desc,
		StartedAt:   time.Now().UTC(),
		cancel:      cancel,
	}
	task.Status.store("pending")

	m.mu.Lock()
	m.tasks[id] = task
	m.mu.Unlock()

	go func() {
		defer cancel()

		task.Status.store("running")
		result, err := fn(taskCtx)

		completedAt := time.Now().UTC()

		m.mu.Lock()

		task.Result = result
		task.CompletedAt = &completedAt

		if taskCtx.Err() == context.Canceled {
			task.Status.store("cancelled")
		} else if err != nil {
			task.Status.store("failed")
			task.Error = err
		} else {
			task.Status.store("completed")
		}

		m.mu.Unlock()
	}()

	return task
}

// Get retrieves a task by its identifier.
//
// Expected:
//   - id is the task identifier to look up.
//
// Returns:
//   - The BackgroundTask and true if found, or nil and false otherwise.
//
// Side effects:
//   - None.
func (m *BackgroundTaskManager) Get(id string) (*BackgroundTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	task, ok := m.tasks[id]
	return task, ok
}

// Cancel requests cancellation of a running task by its identifier.
//
// Expected:
//   - id is the task identifier to cancel.
//
// Returns:
//   - An error if the task does not exist or is not cancellable.
//
// Side effects:
//   - Calls the context cancel function for the task.
func (m *BackgroundTaskManager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[id]
	if !ok {
		return errTaskNotFound
	}

	status := task.Status.Load()
	if status != "pending" && status != "running" {
		return errTaskNotCancellable
	}

	task.cancel()
	return nil
}

// List returns all tracked tasks.
//
// Returns:
//   - A slice of all BackgroundTask values currently tracked.
//
// Side effects:
//   - None.
func (m *BackgroundTaskManager) List() []*BackgroundTask {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]*BackgroundTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}

	return tasks
}

// ActiveCount returns the number of tasks currently in pending or running state.
//
// Returns:
//   - The count of active (non-terminal) tasks.
//
// Side effects:
//   - None.
func (m *BackgroundTaskManager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, task := range m.tasks {
		status := task.Status.Load()
		if status == "pending" || status == "running" {
			count++
		}
	}

	return count
}

var (
	errTaskNotFound       = errTaskNotFoundFn()
	errTaskNotCancellable = errTaskNotCancellableFn()
)

// errTaskNotFoundFn returns the sentinel error used when a task cannot be located.
//
// Returns:
//   - An error indicating the task was not found.
//
// Side effects:
//   - None.
func errTaskNotFoundFn() error {
	return errors.New("task not found")
}

// errTaskNotCancellableFn returns the sentinel error used when a task cannot be cancelled.
//
// Returns:
//   - An error indicating the task cannot be cancelled.
//
// Side effects:
//   - None.
func errTaskNotCancellableFn() error {
	return errors.New("task is not cancellable")
}
