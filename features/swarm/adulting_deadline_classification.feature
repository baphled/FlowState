@swarm @adulting @deadline
Feature: Deadline classification with deterministic dates
  As the deadline-scanner agent
  I want deadlines classified relative to a fixed anchor date
  So that classification is deterministic and testable

  Background:
    Given the adulting test dataset is loaded with anchor date 2026-05-02

  Scenario: Overdue items are classified correctly
    Then the task "Pay council tax arrears" should have deadline class "overdue"

  Scenario: Critical items within 7 days are classified correctly
    Then the task "Submit self-assessment tax return" should have deadline class "critical"
    And the task "Pay electricity bill" should have deadline class "critical"

  Scenario: Imminent items within 14 days are classified correctly
    Then the task "Renew driving licence" should have deadline class "imminent"
    And the task "Book dentist appointment" should have deadline class "imminent"

  Scenario: Approaching items within 28 days are classified correctly
    Then the task "Pay car insurance" should have deadline class "approaching"

  Scenario: Scheduled items beyond 28 days are classified correctly
    Then the task "Renew passport" should have deadline class "scheduled"
    And the task "File annual accounts" should have deadline class "scheduled"

  Scenario: Tasks without deadlines are classified as unspecified
    Then the task "Organise garage" should have deadline class "unspecified"

  Scenario: Bill-related tasks are identified correctly
    Then the tasks should include the following bill items:
      | title                          |
      | Pay council tax arrears        |
      | Submit self-assessment tax return |
      | Pay electricity bill           |
      | Pay car insurance              |

  Scenario: Bill statuses reflect deadline proximity
    Then the bill "Pay council tax arrears" should have status "overdue"
    And the bill "Submit self-assessment tax return" should have status "due-soon"
    And the bill "Pay electricity bill" should have status "due-soon"
    And the bill "Pay car insurance" should have status "upcoming"

  Scenario: Critical path items are flagged correctly
    Then the following tasks should be on the critical path:
      | title                          |
      | Pay council tax arrears        |
      | Submit self-assessment tax return |
      | Pay electricity bill           |
