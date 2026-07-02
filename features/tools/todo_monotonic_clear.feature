@todomono
Feature: Todo monotonic state machine and list clearing
  As a user
  I want todo state transitions to be forward-only and completed lists to be clearable
  So that agents cannot silently revert finished work and can start a fresh list when the current one is done

  Background:
    Given FlowState is running
    And the todo tools are enabled

  Scenario: todo_clear wipes the list so a fresh todowrite succeeds
    Given a session has a todo list with items:
      | content    | status    | priority |
      | first step | completed | high     |
      | last step  | completed | medium   |
    When the agent clears the todo list
    Then the stored todo list should be empty
    And a fresh todowrite should create a new list

  Scenario: todo_update rejects reverting a completed item to pending
    Given a session has a todo list with items:
      | content     | status    | priority |
      | finished    | completed | high     |
      | not started | pending   | medium   |
    When the agent updates todo at index 0 to status "pending"
    Then the update should be rejected with "terminal state"
    And the todo list should be unchanged

  Scenario: todo_update rejects reverting a cancelled item
    Given a session has a todo list with items:
      | content | status    | priority |
      | dropped | cancelled | low      |
      | next    | pending   | medium   |
    When the agent updates todo at index 0 to status "in_progress"
    Then the update should be rejected with "terminal state"
    And the todo list should be unchanged

  Scenario: todo_update rejects reverting an in_progress item to pending
    Given a session has a todo list with items:
      | content | status      | priority |
      | active  | in_progress | high     |
      | queued  | pending     | medium   |
    When the agent updates todo at index 0 to status "pending"
    Then the update should be rejected with "cannot move to"
    And the todo list should be unchanged

  Scenario: todo_update rejects starting a second in_progress item while another is active
    Given a session has a todo list with items:
      | content | status      | priority |
      | active  | in_progress | high     |
      | queued  | pending     | medium   |
    When the agent updates todo at index 1 to status "in_progress"
    Then the update should be rejected with "another todo is already in_progress"
    And the todo list should be unchanged

  Scenario: todo_update allows a pending item to jump directly to completed
    Given a session has a todo list with items:
      | content  | status  | priority |
      | skip me  | pending | low      |
      | carry on | pending | medium   |
    When the agent updates todo at index 0 to status "completed"
    Then todo at index 0 should have status "completed"
    And todo at index 1 should have status "in_progress"

  Scenario: todo_update auto-advances the next pending item when the active item is cancelled
    Given a session has a todo list with items:
      | content | status      | priority |
      | active  | in_progress | high     |
      | queued  | pending     | medium   |
    When the agent updates todo at index 0 to status "cancelled"
    Then todo at index 0 should have status "cancelled"
    And todo at index 1 should have status "in_progress"
