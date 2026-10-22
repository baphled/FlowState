@swarm @persistence-completeness
Feature: Swarm persistence and completeness pre-check
  As the swarm orchestrator
  I want every gated member's coordination-store output verified before synthesis
  So that member outputs cannot be silently lost or left empty at delivery time

  Background:
    Given the persistence-completeness gate runner is registered

  Scenario: Missing coordination-store key fails the gate
    When gated member "Tech-Lead" has no coordination-store entry at "dd-swarm/tech-lead/verdict"
    Then the persistence-completeness gate should fail
    And the failure should name the member "Tech-Lead"
    And the failure should name the key "dd-swarm/tech-lead/verdict"

  Scenario: Empty coordination-store entry fails the gate
    When gated member "Tech-Lead" has an empty coordination-store entry at "dd-swarm/tech-lead/verdict"
    Then the persistence-completeness gate should fail
    And the failure should name the key "dd-swarm/tech-lead/verdict"

  Scenario: Non-empty coordination-store entry passes the gate
    When gated member "Tech-Lead" has a non-empty coordination-store entry at "dd-swarm/tech-lead/verdict"
    Then the persistence-completeness gate should pass

  Scenario: Coordinator-mediated persistence passes only with an explicit reason
    When gated member "Tech-Lead" has no coordination-store entry at "dd-swarm/tech-lead/verdict"
    And the coordination store records a coordinator-mediated write for member "Tech-Lead" with reason "member session lacked coordination_store tool"
    Then the persistence-completeness gate should pass

  Scenario: Coordinator-mediated persistence without a reason fails
    When gated member "Tech-Lead" has no coordination-store entry at "dd-swarm/tech-lead/verdict"
    And the coordination store records a coordinator-mediated write for member "Tech-Lead" with reason ""
    Then the persistence-completeness gate should fail
