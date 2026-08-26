Feature: Configuration Loading
  As a FlowState operator
  I want FlowState to load configuration from sensible sources
  So that the application starts with the expected settings

  @smoke
  Scenario: Load default configuration when no file exists
    Given no FlowState configuration file exists
    When FlowState loads its configuration
    Then the default configuration should be used

  @smoke
  Scenario: Load configuration from a file path
    Given a FlowState configuration file exists at "/tmp/flowstate/config.yaml"
    When FlowState loads configuration from that file path
    Then the configuration from that file should be used

  Scenario: Loaded configuration includes the expected settings
    Given FlowState has loaded its configuration
    Then the configuration should include provider settings
    And the configuration should include an agent directory
    And the configuration should include a skill directory
    And the configuration should include a data directory
    And the configuration should include a log level
