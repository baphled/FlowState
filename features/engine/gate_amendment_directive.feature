@gate-amendment
Feature: Gate Failure Amendment Directive
  appendGateDirective turns a post-member gate failure into the
  retry message the lead re-dispatches. The directive must name
  the gate, surface the structured Reason (so custom DD schema
  messages reach the member), and direct the member to revise
  and re-write to the coordination_store.

  Scenario: No-output failure includes coordination_store guidance
    Given a gate failure where the member wrote no output
    When appendGateDirective constructs the retry message
    Then the directive mentions coordination_store

  Scenario: Schema-validation failure includes gate name and reason
    Given a gate failure from dd-report-section-v1 with reason "120 words, minimum 300"
    When appendGateDirective constructs the retry message
    Then the directive contains "dd-report-section-v1"
    And the directive contains "120 words"

  Scenario: Empty GateError.Reason returns original message unchanged
    Given a gate failure with empty Reason
    When appendGateDirective is called
    Then the message is returned unchanged
