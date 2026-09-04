@swarm @target-specificity
Feature: Target-specificity gate
  As the swarm orchestrator
  I want gated member output to reference target-specific evidence
  So that generic boilerplate cannot satisfy keyword-coverage style gates

  Background:
    Given the target-specificity gate runner is registered

  Scenario: Generic output with no target references fails the gate
    When a member submits payload "The security posture is mixed with several risks identified across authentication, authorisation, and secrets handling. Verdict: low confidence."
    And the engagement brief supplies target identifiers "n-vyro.io, internal/api/auth.go, EVIDENCE-004"
    Then the target-specificity gate should fail
    And the failure should mention target-specific evidence

  Scenario: Output referencing a target identifier passes the gate
    When a member submits payload "Authentication in internal/api/auth.go has no rate limiting (see EVIDENCE-004). n-vyro.io login flow is affected."
    And the engagement brief supplies target identifiers "n-vyro.io, internal/api/auth.go, EVIDENCE-004"
    Then the target-specificity gate should pass

  Scenario: Generic security vocabulary alone does not satisfy the gate
    When a member submits payload "OWASP risks include injection, broken access control, and misconfiguration. Severity is high."
    And the engagement brief supplies target identifiers "n-vyro.io, internal/api/auth.go, EVIDENCE-004"
    Then the target-specificity gate should fail

  Scenario: No configured target identifiers disables the check backward-compatibly
    When a member submits payload "Generic output with no references at all."
    And the engagement brief supplies no target identifiers
    Then the target-specificity gate should pass
