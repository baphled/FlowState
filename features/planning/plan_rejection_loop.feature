@rejection
Feature: Plan rejection and regeneration loop
  As the planning system
  I want rejected plans to trigger deterministic regeneration
  So that plan quality is enforced without relying on LLM instructions

  @rejection @planning-wip
  Scenario: Reviewer rejects plan once then approves regenerated plan
    Given a planning session is in progress
    And the plan-writer has produced a plan
    When the plan-reviewer returns a REJECT verdict
    Then the planner re-delegates to the plan-writer
    And the plan-writer produces a new plan
    And the plan-reviewer returns an APPROVE verdict
    And the final plan is saved

  @rejection @planning-wip
  Scenario: Reviewer rejects plan three times triggering escalation
    Given a planning session is in progress
    And the plan-writer has produced a plan
    When the plan-reviewer rejects the plan 3 consecutive times
    Then the delegate tool returns an errMaxRejectionsExhausted error
    And the planner escalates to the user with the rejection reason
