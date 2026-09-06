Feature: Session resilience against abrupt stream deaths
  As a user of FlowState
  I want transient provider stream failures to be retried before my session is marked failed
  So that a wire cut or provider hiccup does not kill an otherwise healthy session

  Background:
    Given FlowState is running

  Scenario: Stream truncated mid-output is retried before failing the session
    Given a provider stream that cuts off after emitting content but before any stop reason
    When the engine processes the stream for the session
    Then the engine retries the provider stream
    And the retry reason is "stream_truncated"
    And the session is not marked failed while retries remain

  Scenario: Stream truncation retries are capped
    Given a provider stream that cuts off after emitting content on every attempt
    When the engine processes three truncated attempts for the session
    Then the engine stops retrying the stream
    And the session may be marked failed with the truncation reason
