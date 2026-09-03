@wip @quit @dispatch @engine @background-tasks
Feature: Quit-means-quit — cancelling root context stops all processing

  When the user quits (Ctrl+C, /exit, TUI close, or process signal),
  every in-flight operation must stop. No orphan goroutines, no
  post-quit processing, no background tasks left running.

  The key decoupling point is `context.WithoutCancel` in the dispatcher,
  which intentionally lets HTTP handlers return early while streams
  finish. On quit, a separate mechanism must reach through that
  decoupling and terminate everything.

  @wip @quit
  Scenario: Cancelling the root context stops all in-flight sessioned dispatches
    Given a dispatcher with an active sessioned dispatch running under context.WithoutCancel
    When the root application context is cancelled
    Then the sessioned dispatch should terminate without completing further turns
    And no orphan goroutines remain for that session

  @wip @quit
  Scenario: Cancelling the root context stops all in-flight ephemeral dispatches
    Given a dispatcher with an active ephemeral dispatch running under context.WithoutCancel
    When the root application context is cancelled
    Then the in-flight ephemeral stream should terminate
    And the EphemeralHandle.Done channel should emit a cancellation error

  @wip @quit
  Scenario: Cancelling the root context cancels all pending background tasks
    Given a background task manager with running and pending tasks
    When the root application context is cancelled
    Then all pending and running background tasks should be cancelled
    And BackgroundTaskManager.CancelAll should have been called

  @wip @quit
  Scenario: Cancelling the root context drains queued session prompts
    Given a dispatcher with queued prompts for multiple sessions
    When the root application context is cancelled
    Then all session queues should be closed
    And no queued prompt should execute after cancellation

  @wip @quit
  Scenario: Engine shutdown cancels in-flight streams via root context
    Given an engine with an active stream
    When Engine.Shutdown is called
    Then onStreamCancel should fire for the active session

  @wip @quit
  Scenario: App shutdown cancels all processing
    Given an application with an active engine, dispatcher, and background task manager
    When App.Shutdown is called
    Then the engine should be shut down
    And all background tasks should be cancelled
    And all session queues should be drained
    And no orphan goroutines remain
