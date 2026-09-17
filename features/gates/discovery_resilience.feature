@gate-registration
Feature: Gate Discovery Resilience
  One malformed sibling manifest must not unregister every ext gate.
  Discovery skips malformed manifests, still returns the valid ones,
  and surfaces each skipped path so boot can name the offending gate
  directory in an actionable error.

  Scenario: Malformed sibling manifest does not abort discovery
    Given a gates directory containing one malformed and one valid manifest
    When gates are discovered from the directory
    Then the valid manifest is returned
    And a partial error names the malformed directory

  Scenario: Valid-only gates directory discovers without error
    Given a gates directory containing only a valid manifest
    When gates are discovered from the directory
    Then the valid manifest is returned
    And no partial errors are collected

  Scenario: Boot registration keeps valid gates when a sibling manifest is malformed
    Given a gates directory containing one malformed and one valid manifest
    When discovered gates are registered at boot
    Then the valid gate is registered
    And boot remains non-fatal
    And the registration failure names the malformed directory

  Scenario: Boot registration failure is reported at error level
    Given a gates directory containing one malformed manifest
    When discovered gates are registered at boot
    Then the failure is logged at error level with the gate directory named
