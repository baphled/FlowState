@failover @equivalent-rotation
Feature: Equivalent provider rotation

  Same-tier equivalent providers should rotate by least recently used order,
  while different tiers stay on their own side of the boundary.

  Scenario: Two healthy same-tier equivalent providers rotate by least recently used order
    Given the following equivalent providers are configured:
      | provider | model  | tier   | last_used |
      | openai   | gpt-4o | tier-1 | 2         |
      | openzen  | gpt-4o | tier-1 | 1         |
    When the rotation order is calculated
    Then the rotation order should be:
      | provider | model  |
      | openzen  | gpt-4o |
      | openai   | gpt-4o |
    And the rotation should be based on least recently used

  Scenario: Providers in different tiers are never rotated across the tier boundary
    Given the following equivalent providers are configured:
      | provider  | model               | tier   | last_used |
      | anthropic | claude-sonnet-4     | tier-0 | 2         |
      | openai    | gpt-4o              | tier-1 | 1         |
    When the rotation order is calculated
    Then the rotation order should be:
      | provider  | model               |
      | anthropic | claude-sonnet-4     |
      | openai    | gpt-4o              |
    And providers in different tiers should not rotate across the tier boundary

  Scenario: A tier with a single provider stays unchanged
    Given the following equivalent providers are configured:
      | provider | model  | tier   | last_used |
      | openai   | gpt-4o | tier-1 | 1         |
    When the rotation order is calculated
    Then the rotation order should be:
      | provider | model  |
      | openai   | gpt-4o |
    And the single-provider tier ordering should stay unchanged
