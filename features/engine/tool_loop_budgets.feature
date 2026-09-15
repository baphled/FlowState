@engine
Feature: Tool-loop budgets count tool execution time

  Scenario: Non-delegation tool time counts toward the duration cap
    Given an engine with a tool loop duration cap of 200ms
    And a tool that sleeps 50ms per call and never finishes the task
    When the cumulative tool execution time exceeds the cap
    Then the tool loop is capped with reason "total_tool_time_backstop"

  Scenario: Delegated execution time is exempt from the parent tool-time cap
    Given an engine with a tool loop duration cap of 200ms
    And a delegation tool whose child engine runs for 500ms
    When the delegation completes
    Then the parent tool loop does not trip the tool-time backstop
