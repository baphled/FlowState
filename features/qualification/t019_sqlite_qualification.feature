Feature: T019 SQLite driver qualification
  Qualify a pure-Go SQLite driver (modernc.org/sqlite) for FlowState M1 storage:
  WAL pragmas, versioned forward-only migrations, a single serialized event
  writer, mid-turn cancellation, durable budget pause/resume, canonical JSON
  and a replay oracle.

  Background:
    Given an isolated temporary database opened with the qualified driver
    And the effective pragmas are recorded as journal_mode=wal foreign_keys=1 synchronous=1 or 2
    And the runtime versions are recorded

  @t019 @smoke
  Scenario: Migrations apply forward-only and reapply is a no-op
    When migrations are applied to the empty database
    Then the schema_migrations table lists versions 1
    And reapplying migrations is a no-op with all tables still present

  @t019 @smoke
  Scenario: Single writer serializes streamed deltas and tool interleave
    When a session is created with a single event writer
    And the writer accepts a deterministic fake stream of 200 provider deltas with interleaved tool calls and results
    And concurrent readers query the events table while the writer is running
    Then the event sequence is contiguous from 0 to max with no duplicates
    And every event carries the correct session and actor attribution

  @t019 @smoke
  Scenario: Cancel mid-turn commits atomically and rejects late effects
    When a session is created with a single event writer
    And a turn is cancelled mid-stream after several provider deltas
    Then the cancellation checkpoint and terminal session.cancelled event are committed atomically
    And the session status is cancelled after reopening the database
    And late provider deltas and tool results after cancellation are rejected
    And the event sequence is contiguous from 0 to max with no gaps

  @t019 @smoke
  Scenario: Durable budget pause and explicit resume
    When a session is created with a single event writer
    And the budget is exhausted mid-turn with an uncertain tool effect outstanding
    Then a session.paused event is committed and the session status is paused and nonterminal
    And no turn.end event is emitted for the pause
    And after reopening the database an explicit resume appends session.resumed and moves status to active
    And the resume does not repeat the uncertain tool effect

  @t019 @smoke
  Scenario: Canonical JSON payloads verify on replay
    When a session is created with a single event writer
    And a mixed stream of events with unicode text and float and integer payloads is written
    Then every payload_json is canonical with sorted keys and no insignificant whitespace
    And sha256 of every payload_json equals its payload_sha256
    And replay on a reopened database verifies all digests and reconstructs the transcript

  @t019 @smoke
  Scenario: Corrupted digest or sequence fails the replay oracle
    When a session is created with a single event writer
    And a stream of events is written
    And the payload_sha256 of one row is deliberately corrupted
    Then the replay oracle fails verification
    And a separately corrupted seq that reorders or duplicates events also fails the replay oracle
