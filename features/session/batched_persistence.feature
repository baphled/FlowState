Feature: Batched session persistence
  As a FlowState operator
  I want session sidecar writes batched across message appends
  So that large sessions stop paying a full-history marshal + fsync per message

  Background:
    Given FlowState is running

  @persistence
  Scenario: Appends below the mutation threshold defer the sidecar write
    When I append 5 messages to a persisted session
    Then the session sidecar has not yet been written
    And the in-memory session has 5 messages

  @persistence
  Scenario: Reaching the mutation threshold flushes the sidecar
    When I append 20 messages to a persisted session
    Then the session sidecar contains 20 messages

  @persistence
  Scenario: FlushPendingPersists writes the full history on demand
    When I append 3 messages to a persisted session
    And I flush pending session persists
    Then the session sidecar contains 3 messages

  @persistence
  Scenario: Closing a session flushes pending appends immediately
    When I append 3 messages to a persisted session
    And I close the session
    Then the session sidecar contains 3 messages
    And the session sidecar status is completed
