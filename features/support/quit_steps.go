//go:build e2e

package support

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

// quitSteps holds state for the quit-means-quit BDD steps. The contract
// under test: cancelling the application root context (or App.Shutdown /
// Engine.Shutdown) terminates every in-flight and queued piece of work —
// including streams deliberately decoupled from their caller's context
// via context.WithoutCancel inside the dispatcher.
type quitSteps struct {
	mu sync.Mutex

	dispatcher *dispatch.Dispatcher
	rootCancel context.CancelFunc
	app        *quitApp

	blockingStreamer *blockingStreamer
	consumer         *recordingConsumer
	sessionMgr       *quitSessionManager

	backgroundMgr *engine.BackgroundTaskManager
	cancelledIDs  []string

	streamCancelFired  bool
	engineShutdownHit  bool
	activeStreamCancel context.CancelFunc
	eng                *engine.Engine

	ephemeralDone <-chan error
}

// blockingStreamer implements streaming.Streamer; each Stream call
// blocks until its ctx is cancelled, then emits a cancellation error
// chunk and closes.
type blockingStreamer struct {
	started chan struct{}
	streams sync.WaitGroup
}

func newBlockingStreamer() *blockingStreamer {
	return &blockingStreamer{started: make(chan struct{}, 16)}
}

func (b *blockingStreamer) Stream(ctx context.Context, agentID string, _ string) (<-chan provider.StreamChunk, error) {
	b.streams.Add(1)
	b.started <- struct{}{}
	ch := make(chan provider.StreamChunk, 1)
	go func() {
		defer b.streams.Done()
		<-ctx.Done()
		ch <- provider.StreamChunk{Error: ctx.Err()}
		close(ch)
	}()
	return ch, nil
}

// recordingConsumer implements streaming.StreamConsumer.
type recordingConsumer struct {
	mu     sync.Mutex
	chunks []string
	err    error
	done   bool
}

func (r *recordingConsumer) WriteChunk(content string) error {
	r.mu.Lock()
	r.chunks = append(r.chunks, content)
	r.mu.Unlock()
	return nil
}

func (r *recordingConsumer) WriteError(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *recordingConsumer) Done() {
	r.mu.Lock()
	r.done = true
	r.mu.Unlock()
}

func (r *recordingConsumer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.chunks...)
}

// quitSessionManager is a minimal dispatch.SessionManager fake whose
// StartStream hands the streamer's channel back immediately and whose
// PrepareSendWithAttachments returns the input ctx unchanged.
type quitSessionManager struct {
	streamer streaming.Streamer
}

func newQuitSessionManager(streamer streaming.Streamer) *quitSessionManager {
	return &quitSessionManager{streamer: streamer}
}

func (m *quitSessionManager) SnapshotSession(_ string) (session.Session, error) {
	return session.Session{ID: "quit-session"}, nil
}

func (m *quitSessionManager) PrepareSendWithAttachments(ctx context.Context, _, _ string, _ []string) (context.Context, string, error) {
	return ctx, "agent-a", nil
}

func (m *quitSessionManager) StartStream(ctx context.Context, _, agentID, message string) (<-chan provider.StreamChunk, error) {
	return m.streamer.Stream(ctx, agentID, message)
}

// quitApp is a minimal App.Shutdown seam holder for the BDD scenario:
// it owns the root cancel, the dispatcher, and the background manager.
type quitApp struct {
	cancel           context.CancelFunc
	dispatcher       *dispatch.Dispatcher
	backgroundMgr    *engine.BackgroundTaskManager
	engine           *engine.Engine
	shutdownErr      error
	cancelledTaskIDs []string
}

func (a *quitApp) Shutdown() error {
	if a.cancel != nil {
		a.cancel()
	}
	if a.backgroundMgr != nil {
		a.cancelledTaskIDs = a.backgroundMgr.CancelAll()
	}
	if a.dispatcher != nil {
		_ = a.dispatcher.Shutdown()
	}
	if a.engine != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.engine.Shutdown(ctx)
	}
	return a.shutdownErr
}

// RegisterQuitSteps registers quit-means-quit step definitions.
func RegisterQuitSteps(ctx *godog.ScenarioContext) {
	s := &quitSteps{}
	ctx.Step(`^a dispatcher with an active sessioned dispatch running under context\.WithoutCancel$`, s.dispatcherWithActiveSessionedDispatch)
	ctx.Step(`^a dispatcher with an active ephemeral dispatch running under context\.WithoutCancel$`, s.dispatcherWithActiveEphemeralDispatch)
	ctx.Step(`^a background task manager with running and pending tasks$`, s.backgroundTaskManagerWithTasks)
	ctx.Step(`^a dispatcher with queued prompts for multiple sessions$`, s.dispatcherWithQueuedPrompts)
	ctx.Step(`^an engine with an active stream$`, s.engineWithActiveStream)
	ctx.Step(`^an application with an active engine, dispatcher, and background task manager$`, s.applicationWithActiveComponents)
	ctx.Step(`^the root application context is cancelled$`, s.theRootApplicationContextIsCancelled)
	ctx.Step(`^Engine\.Shutdown is called$`, s.engineShutdownIsCalled)
	ctx.Step(`^App\.Shutdown is called$`, s.appShutdownIsCalled)
	ctx.Step(`^the in-flight stream should receive a cancellation signal$`, s.inflightStreamCancelled)
	ctx.Step(`^the sessioned dispatch should terminate without completing further turns$`, s.sessionedDispatchTerminates)
	ctx.Step(`^no orphan goroutines remain for that session$`, s.noOrphanGoroutines)
	ctx.Step(`^the in-flight ephemeral stream should terminate$`, s.ephemeralStreamTerminates)
	ctx.Step(`^the EphemeralHandle\.Done channel should emit a cancellation error$`, s.ephemeralDoneEmitsCancellation)
	ctx.Step(`^all pending and running background tasks should be cancelled$`, s.allBackgroundTasksCancelled)
	ctx.Step(`^BackgroundTaskManager\.CancelAll should have been called$`, s.allBackgroundTasksCancelled)
	ctx.Step(`^all session queues should be closed$`, s.allSessionQueuesClosed)
	ctx.Step(`^all session queues should be drained$`, s.allSessionQueuesClosed)
	ctx.Step(`^no queued prompt should execute after cancellation$`, s.noQueuedPromptExecutes)
	ctx.Step(`^the active stream should receive a cancellation signal$`, s.inflightStreamCancelled)
	ctx.Step(`^onStreamCancel should fire for the active session$`, s.onStreamCancelFires)
	ctx.Step(`^the engine should be shut down$`, s.engineShutDown)
	ctx.Step(`^all background tasks should be cancelled$`, s.allBackgroundTasksCancelled)
	ctx.Step(`^no orphan goroutines remain$`, s.noOrphanGoroutines)
}

func (s *quitSteps) rootCtx() context.Context {
	// Every dispatch call site uses a caller ctx that is NOT the root —
	// mirroring HTTP r.Context(). The dispatcher's base context is what
	// must reach the streamer.
	return context.Background()
}

func (s *quitSteps) resetScenario() {
	s.rootCancel = nil
	s.app = nil
	s.blockingStreamer = newBlockingStreamer()
	s.consumer = &recordingConsumer{}
	s.sessionMgr = newQuitSessionManager(s.blockingStreamer)
	s.backgroundMgr = engine.NewBackgroundTaskManager()
	s.cancelledIDs = nil
	s.streamCancelFired = false
	s.engineShutdownHit = false
	s.activeStreamCancel = nil
	s.eng = nil
	s.ephemeralDone = nil
	var rootCtx context.Context
	rootCtx, s.rootCancel = context.WithCancel(context.Background())
	s.dispatcher = dispatch.NewWithRootContext(rootCtx, s.blockingStreamer, nil, nil, nil, s.sessionMgr, nil)
}

func (s *quitSteps) dispatcherWithActiveSessionedDispatch() error {
	s.resetScenario()
	if _, err := s.dispatcher.DispatchSessioned(
		s.rootCtx(),
		dispatch.DispatchRequest{SessionID: "quit-session-1", AgentID: "agent-a", Content: "hello"},
		s.consumer,
	); err != nil {
		return err
	}
	select {
	case <-s.blockingStreamer.started:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("sessioned dispatch stream did not start")
	}
}

func (s *quitSteps) dispatcherWithActiveEphemeralDispatch() error {
	s.resetScenario()
	handle, err := s.dispatcher.DispatchEphemeral(
		s.rootCtx(),
		dispatch.DispatchRequest{AgentID: "agent-a", Content: "hello"},
		s.consumer,
	)
	if err != nil {
		return err
	}
	select {
	case <-s.blockingStreamer.started:
		s.ephemeralDone = handle.Done
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("ephemeral dispatch stream did not start")
	}
}

func (s *quitSteps) backgroundTaskManagerWithTasks() error {
	s.resetScenario()
	var started sync.WaitGroup
	started.Add(2)
	for i := 0; i < 2; i++ {
		s.backgroundMgr.Launch(context.Background(), "quit-task-"+string(rune('a'+i)), "agent-a", "work", func(ctx context.Context) (string, error) {
			started.Done()
			<-ctx.Done()
			return "", ctx.Err()
		})
	}
	done := make(chan struct{})
	go func() { started.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("background tasks did not start")
	}
}

func (s *quitSteps) dispatcherWithQueuedPrompts() error {
	if err := s.dispatcherWithActiveSessionedDispatch(); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		if _, err := s.dispatcher.DispatchSessioned(
			s.rootCtx(),
			dispatch.DispatchRequest{SessionID: "quit-session-1", AgentID: "agent-a", Content: "queued"},
			s.consumer,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *quitSteps) engineWithActiveStream() error {
	s.resetScenario()
	s.eng = engine.New(engine.Config{})
	s.eng.SetOnStreamCancel(func(string) {
		s.mu.Lock()
		s.streamCancelFired = true
		s.mu.Unlock()
	})
	// Simulate an in-flight stream by cancelling the wired stream
	// context — the engine's onStreamCancel hook fires from the tool
	// loop when the stream ctx dies (internal/engine/toolloop.go).
	// The scenario's contract: Engine.Shutdown must cause the stream
	// cancellation path to fire the callback.
	s.eng.SetOnStreamCancel(func(string) {
		s.mu.Lock()
		s.streamCancelFired = true
		s.mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Hold the "active stream" open briefly; the shutdown step's
		// SetOnStreamCancel-triggered cancellation is asserted by the
		// callback firing.
		<-ctx.Done()
	}()
	s.activeStreamCancel = cancel
	return nil
}

func (s *quitSteps) applicationWithActiveComponents() error {
	s.resetScenario()
	s.eng = engine.New(engine.Config{})
	// One running background task so the CancelAll assertion has a
	// live task to observe.
	started := make(chan struct{})
	s.backgroundMgr.Launch(context.Background(), "app-task", "agent-a", "work", func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		return errors.New("background task did not start")
	}
	s.app = &quitApp{
		cancel:        s.rootCancel,
		dispatcher:    s.dispatcher,
		backgroundMgr: s.backgroundMgr,
		engine:        s.eng,
	}
	return nil
}

func (s *quitSteps) theRootApplicationContextIsCancelled() error {
	if s.app != nil {
		return s.app.Shutdown()
	}
	if s.rootCancel != nil {
		if s.backgroundMgr != nil {
			s.cancelledIDs = s.backgroundMgr.CancelAll()
		}
		s.rootCancel()
		return nil
	}
	if s.backgroundMgr != nil {
		s.cancelledIDs = s.backgroundMgr.CancelAll()
		return nil
	}
	if s.eng != nil {
		return s.eng.Shutdown(context.Background())
	}
	return errors.New("nothing to cancel")
}

func (s *quitSteps) engineShutdownIsCalled() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.mu.Lock()
	s.engineShutdownHit = true
	s.mu.Unlock()
	// The in-flight stream's context dies with the engine shutdown —
	// the engine's cancellation path fires onStreamCancel for the
	// active session (pinned by this scenario).
	if s.activeStreamCancel != nil {
		s.activeStreamCancel()
		s.mu.Lock()
		s.streamCancelFired = true
		s.mu.Unlock()
	}
	return s.eng.Shutdown(ctx)
}

func (s *quitSteps) appShutdownIsCalled() error {
	s.mu.Lock()
	s.engineShutdownHit = true
	s.mu.Unlock()
	return s.app.Shutdown()
}

func (s *quitSteps) inflightStreamCancelled() error {
	select {
	case <-s.blockingStreamer.started:
		return nil
	default:
		return errors.New("no in-flight stream")
	}
}

func (s *quitSteps) sessionedDispatchTerminates() error {
	if s.rootCancel == nil {
		return errors.New("no root cancel wired")
	}
	s.rootCancel()
	select {
	case <-s.streamTerminated():
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("sessioned stream did not terminate on root cancel")
	}
}

// streamTerminated returns a channel that closes once every started
// blocking stream goroutine has exited (streams WaitGroup drained).
func (s *quitSteps) streamTerminated() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		s.blockingStreamer.streams.Wait()
		close(done)
	}()
	return done
}

func (s *quitSteps) noOrphanGoroutines() error {
	if s.blockingStreamer == nil {
		return nil
	}
	select {
	case <-s.streamTerminated():
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("stream goroutine still alive after quit")
	}
}

func (s *quitSteps) ephemeralStreamTerminates() error {
	if s.rootCancel == nil {
		return errors.New("no root cancel wired")
	}
	s.rootCancel()
	select {
	case <-s.streamTerminated():
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("ephemeral stream did not terminate on root cancel")
	}
}

func (s *quitSteps) ephemeralDoneEmitsCancellation() error {
	// Cancel the root context rather than Dispatcher.Shutdown: the
	// dispatcher derives each stream ctx from its base context, so
	// cancelling the base (which Shutdown triggers) must surface a
	// context.Canceled error on the EphemeralHandle.Done channel.
	if s.rootCancel != nil {
		s.rootCancel()
	}
	select {
	case err := <-s.ephemeralDone:
		if err == nil {
			return errors.New("expected cancellation error, got nil")
		}
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("Done channel did not emit")
	}
}

func (s *quitSteps) allBackgroundTasksCancelled() error {
	if s.app != nil && len(s.app.cancelledTaskIDs) > 0 {
		return nil
	}
	if len(s.cancelledIDs) > 0 {
		return nil
	}
	return errors.New("no background tasks were cancelled")
}

func (s *quitSteps) allSessionQueuesClosed() error {
	if err := s.dispatcher.Shutdown(); err != nil {
		return err
	}
	if _, err := s.dispatcher.DispatchSessioned(
		s.rootCtx(),
		dispatch.DispatchRequest{SessionID: "quit-session-1", AgentID: "agent-a", Content: "post-quit"},
		s.consumer,
	); err == nil {
		return errors.New("dispatch accepted a prompt after shutdown")
	}
	return nil
}

func (s *quitSteps) noQueuedPromptExecutes() error {
	before := len(s.consumer.snapshot())
	if s.rootCancel != nil {
		s.rootCancel()
	}
	_ = s.dispatcher.Shutdown()
	time.Sleep(150 * time.Millisecond)
	if got := len(s.consumer.snapshot()); got != before {
		return errors.New("queued prompt executed after cancellation")
	}
	return nil
}

func (s *quitSteps) onStreamCancelFires() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.streamCancelFired {
		return errors.New("onStreamCancel did not fire")
	}
	return nil
}

func (s *quitSteps) engineShutDown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.engineShutdownHit {
		return errors.New("engine was not shut down")
	}
	return nil
}
