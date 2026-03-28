package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

// BackgroundTask represents a tracked background execution.
type BackgroundTask struct {
	ID              string
	AgentID         string
	Description     string
	Status          atomicValue
	StartedAt       time.Time
	CompletedAt     *time.Time
	Result          string
	Error           error
	cancel          context.CancelFunc
	ConcurrencyKey  string
	ParentSessionID string
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

const maxConcurrentTasks = 50

// ConcurrencyConfig defines per-key and total concurrency limits.
type ConcurrencyConfig struct {
	MaxPerKey int
	MaxTotal  int
}

// BackgroundTaskManager tracks parallel delegation tasks with per-key and total concurrency limits.
type BackgroundTaskManager struct {
	tasks      map[string]*BackgroundTask
	mu         sync.RWMutex
	config     ConcurrencyConfig
	perKeySems map[string]chan struct{}
	totalSem   chan struct{}
	semsMu     sync.Mutex
	sessionMgr *session.Manager
}

// NewBackgroundTaskManager creates a new task manager with per-key concurrency limiting.
// Default configuration: 3 tasks per key, 50 total.
//
// Returns:
//   - A ready-to-use BackgroundTaskManager instance.
//
// Side effects:
//   - None.
func NewBackgroundTaskManager() *BackgroundTaskManager {
	return &BackgroundTaskManager{
		tasks: make(map[string]*BackgroundTask),
		config: ConcurrencyConfig{
			MaxPerKey: 3,
			MaxTotal:  maxConcurrentTasks,
		},
		perKeySems: make(map[string]chan struct{}),
		totalSem:   make(chan struct{}, maxConcurrentTasks),
	}
}

// WithSessionManager sets the session manager for notification injection.
// Expected:
//   - mgr is a valid session manager or nil to disable notification injection.
//
// Returns:
//   - The BackgroundTaskManager for chaining.
//
// Side effects:
//   - Stores the session manager reference for later use.
func (m *BackgroundTaskManager) WithSessionManager(mgr *session.Manager) *BackgroundTaskManager {
	m.sessionMgr = mgr
	return m
}

// injectCompletionNotification sends a completion notification to the parent session.
// Errors are intentionally not checked as notification delivery is best-effort.
//
// Expected:
//   - sessionID is a valid parent session identifier.
//   - notification is a valid CompletionNotificationEvent.
//
// Side effects:
//   - Calls the session manager's InjectNotification method if configured.
func (m *BackgroundTaskManager) injectCompletionNotification(sessionID string, notification streaming.CompletionNotificationEvent) {
	if m.sessionMgr != nil {
		if err := m.sessionMgr.InjectNotification(sessionID, notification); err != nil {
			return
		}
	}
}

// Launch starts a background task by executing the provided function asynchronously.
// The task is tracked by ID and its status is updated upon completion.
// Concurrency is limited per provider+model key and across all tasks.
//
// Expected:
//   - ctx is a valid context for the background operation.
//   - id is a unique identifier for the task.
//   - agentID identifies the agent that owns this task (used as concurrency key).
//   - desc describes the task for tracking purposes.
//   - fn is the function to execute asynchronously.
//
// Returns:
//   - The created BackgroundTask, already marked as pending.
//
// Side effects:
//   - Spawns a goroutine to execute fn.
//   - Updates task status to "completed", "failed", or "cancelled".
//   - Acquires and releases per-key and total semaphores.
func (m *BackgroundTaskManager) Launch(
	ctx context.Context,
	id, agentID, desc string,
	fn func(ctx context.Context) (string, error),
) *BackgroundTask {
	taskCtx, cancel := context.WithCancel(ctx)
	concurrencyKey := agentID

	task := &BackgroundTask{
		ID:             id,
		AgentID:        agentID,
		Description:    desc,
		StartedAt:      time.Now().UTC(),
		cancel:         cancel,
		ConcurrencyKey: concurrencyKey,
	}
	task.Status.store("pending")

	m.mu.Lock()
	m.tasks[id] = task
	m.mu.Unlock()

	go func() {
		defer cancel()
		m.executeTask(taskCtx, concurrencyKey, task, fn)
	}()

	return task
}

// executeTask acquires semaphores, runs the task function, and updates task status.
//
// Expected:
//   - taskCtx is the task-specific context.
//   - concurrencyKey is the per-key semaphore identifier.
//   - task is the BackgroundTask being executed.
//   - fn is the task function to execute.
//
// Returns:
//   - None.
//
// Side effects:
//   - Acquires and releases per-key and total semaphores.
//   - Updates task status to "running", then to "completed", "failed", or "cancelled".
func (m *BackgroundTaskManager) executeTask(
	taskCtx context.Context,
	concurrencyKey string,
	task *BackgroundTask,
	fn func(ctx context.Context) (string, error),
) {
	m.semsMu.Lock()
	keySem, exists := m.perKeySems[concurrencyKey]
	if !exists {
		keySem = make(chan struct{}, m.config.MaxPerKey)
		m.perKeySems[concurrencyKey] = keySem
	}
	m.semsMu.Unlock()

	keySem <- struct{}{}
	m.totalSem <- struct{}{}
	defer func() {
		<-m.totalSem
		<-keySem
	}()

	m.mu.Lock()
	task.Status.store("running")
	m.mu.Unlock()

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

	if m.sessionMgr != nil && task.ParentSessionID != "" {
		notification := streaming.CompletionNotificationEvent{
			TaskID:      task.ID,
			Description: task.Description,
			Agent:       task.AgentID,
			Duration:    completedAt.Sub(task.StartedAt),
			Status:      task.Status.Load(),
			Result:      task.Result,
		}
		m.injectCompletionNotification(task.ParentSessionID, notification)
	}
}

// Get returns a snapshot copy of the task with the given identifier.
//
// Expected:
//   - id is a non-empty task identifier.
//
// Returns:
//   - A value copy of the BackgroundTask and true if found.
//   - Zero BackgroundTask and false if not found.
//
// Side effects:
//   - None.
func (m *BackgroundTaskManager) Get(id string) (BackgroundTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	task, ok := m.tasks[id]
	if !ok {
		return BackgroundTask{}, false
	}
	return *task, true
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

// CancelAll requests cancellation of all running and pending tasks.
//
// Returns:
//   - A slice of task IDs that were successfully cancelled (empty slice if none cancelled).
//
// Side effects:
//   - Calls the context cancel function for each active task.
func (m *BackgroundTaskManager) CancelAll() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	cancelledIDs := make([]string, 0)

	for id, task := range m.tasks {
		status := task.Status.Load()
		if status == "pending" || status == "running" {
			task.cancel()
			cancelledIDs = append(cancelledIDs, id)
		}
	}

	return cancelledIDs
}

// List returns all tracked tasks.
//
// Returns:
//   - A slice of all BackgroundTask values currently tracked.
//
// Side effects:
//   - None.
//
// List returns snapshot copies of all tracked background tasks.
//
// Expected:
//   - None.
//
// Returns:
//   - A slice of BackgroundTask value copies.
//
// Side effects:
//   - None.
func (m *BackgroundTaskManager) List() []BackgroundTask {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]BackgroundTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, *t)
	}

	return tasks
}

// EvictCompleted removes all terminal-state tasks (completed, failed, cancelled)
// from the internal task map. Running and pending tasks are not affected.
//
// Returns:
//   - None.
//
// Side effects:
//   - Deletes terminal tasks from the tasks map under write lock.
func (m *BackgroundTaskManager) EvictCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, task := range m.tasks {
		status := task.Status.Load()
		if status == "completed" || status == "failed" || status == "cancelled" {
			delete(m.tasks, id)
		}
	}
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
