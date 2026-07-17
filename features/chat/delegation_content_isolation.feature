Feature: Delegation content isolation
  As a user
  I want delegated child-agent content to stay isolated unless teeing is enabled
  So that the coordinator stream shows only parent-visible output by default

  When `delegation.tee_child_content` is off, child-agent chain-of-thought
  stays in the child session and reaches the coordinator only through the
  delegate tool result. When the gate is turned on, the parent stream mirrors
  the child content for legacy behaviour.

  Background:
    Given FlowState is running
    And delegation is enabled

  @chat @delegation-session @wip
  Scenario: Default gate keeps child content isolated from the parent stream
    Given delegation tee_child_content is off by default
    When a coordinator delegates to a sub-agent
    Then the coordinator's content stream should not contain the sub-agent's chain-of-thought
    And the sub-agent's full output should be available in the child session

  @chat @delegation-session @wip
  Scenario: Enabled gate mirrors child content into the parent stream
    Given delegation tee_child_content is on
    When a coordinator delegates to a sub-agent
    Then the coordinator's content stream should contain the sub-agent's chain-of-thought
