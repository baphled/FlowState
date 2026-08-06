@failover @config-chain
Feature: Config-driven provider selection chain

  The failover chain should honour providers.default, exclude providers without
  effective credentials, and surface a clear error when the configured default
  cannot be resolved.

  Scenario: The configured default provider is tried first and only credentialed providers remain eligible
    Given the configured providers are:
      | provider  | model               | credentials | eligible |
      | anthropic | claude-sonnet-4     | absent      | no       |
      | openai    | gpt-4o              | present     | yes      |
      | ollama    | llama3.2            | host+model  | yes      |
    And providers.default is "openai"
    When the failover chain is built
    Then the candidate order should be:
      | provider | model       |
      | openai    | gpt-4o      |
      | ollama    | llama3.2    |
    And the provider "anthropic" / "claude-sonnet-4" should not be present

  Scenario: An unknown default provider produces a clear error
    Given the configured providers are:
      | provider | model  | credentials | eligible |
      | openai   | gpt-4o | present     | yes      |
    And providers.default is "does-not-exist"
    When the failover chain is built
    Then the default provider validation should fail with "unknown provider"

  Scenario: A known but unconfigured default provider produces a clear error
    Given the configured providers are:
      | provider | model  | credentials | eligible |
      | openai   | gpt-4o | present     | yes      |
    And providers.default is "anthropic"
    When the failover chain is built
    Then the default provider validation should fail with "not configured"

  Scenario: If no provider is eligible the chain is empty
    Given the configured providers are:
      | provider  | model           | credentials | eligible |
      | anthropic | claude-sonnet-4 | absent      | no       |
      | openai    | gpt-4o          | absent      | no       |
    And providers.default is "openai"
    When the failover chain is built
    Then the chain should be empty
