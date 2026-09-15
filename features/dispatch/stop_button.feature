@dispatch @session @tools
Feature: Stop button reliability — cancel settles the whole server-side turn

  Background:
    Given FlowState is running

  @smoke
  Scenario: Cancelling a turn blocked on a wedged command settles everything
    Given a session with a running turn blocked on a wedged bash command
    And a second prompt is queued behind the running turn
    When the user cancels the turn via DELETE /turns/{turn_id}
    Then the turn settles as "cancelled" within 15 seconds
    And the session meta.json contains "failure_reason" set to "user_cancelled"
    And the wedged command's process tree is dead
    And the queued prompt is released for dispatch
