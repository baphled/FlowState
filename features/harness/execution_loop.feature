@harness @execution
Feature: Execution Loop
  As a FlowState developer
  I want a general-purpose harness evaluation loop
  So that non-planning agents can benefit from validate-critique cycles

  Background:
    Given the execution loop is configured with max retries 3

  @smoke
  Scenario: Output passes on the first attempt without a validator
    Given no validator is configured
    When the execution loop evaluates agent "executor" with message "do the thing"
    Then the evaluation result has output "result text"
    And the evaluation attempt count is 1
    And the final score is 1.0

  Scenario: Output passes validator on first attempt
    Given a validator that accepts all output
    When the execution loop evaluates agent "executor" with message "check this"
    Then the evaluation succeeds
    And the evaluation attempt count is 1

  Scenario: Output fails validator and exhausts retries
    Given a validator that rejects all output
    And the execution loop is configured with max retries 2
    When the execution loop evaluates agent "executor" with message "bad input"
    Then the evaluation completes without error
    And the evaluation attempt count is 2

  Scenario: Stream evaluate returns chunks then done
    Given no validator is configured
    When the execution loop stream-evaluates agent "executor" with message "stream me"
    Then the stream contains a content chunk "stream result"
    And the stream ends with a done chunk

  Scenario: Context cancellation stops the loop
    Given no validator is configured
    When the execution loop evaluates with a cancelled context
    Then the evaluation completes without error
