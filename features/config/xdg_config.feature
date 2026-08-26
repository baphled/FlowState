@config
Feature: XDG Config Path Support
  Scenario: Config loads from XDG_CONFIG_HOME
    Given XDG_CONFIG_HOME is set to a custom path
    And a config file exists at that path
    When FlowState loads its configuration
    Then the config should be loaded from the XDG path
