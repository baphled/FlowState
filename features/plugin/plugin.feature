Feature: Plugin System
  As a FlowState user
  I want a robust plugin system
  So that I can extend FlowState without risking stability

  Background:
    Given FlowState is configured with an empty plugins directory

  Scenario: Plugin system starts with no external plugins
    When FlowState starts
    Then the plugin system initialises without error
    And no external plugins are loaded

  Scenario: Valid external plugin is discovered and loaded
    Given a valid plugin manifest exists in the plugins directory
    When FlowState starts
    Then the plugin is registered in the plugin registry

  Scenario: Malformed manifest is skipped without crashing
    Given a malformed plugin manifest exists in the plugins directory
    When FlowState starts
    Then FlowState continues running
    And the malformed plugin is not loaded

  Scenario: Plugin crash does not crash FlowState
    Given a valid plugin manifest exists in the plugins directory
    And FlowState has started with the plugin loaded
    When the plugin process crashes
    Then FlowState continues running
    And the crashed plugin is removed from the registry

  Scenario: Provider failover activates on rate limit
    Given FlowState is running with the failover plugin active
    When a provider returns a rate-limit error
    Then the failover hook switches to an alternative provider

  Scenario: Event logger writes events to JSONL
    Given FlowState is running with the event logger active
    When a session is created
    Then an event is written to the event log file
