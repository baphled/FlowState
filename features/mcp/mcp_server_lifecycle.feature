@mcp
Feature: MCP server enablement and lifecycle
  As a FlowState operator
  I want explicit enablement and single-spawn lifecycle semantics for MCP servers
  So that disabled legacy servers stay off and one serve invocation keeps one live server pair

  Scenario: An explicitly disabled MCP server survives config load and is never connected
    Given a FlowState configuration file with MCP server "legacy-vault" set to enabled false
    When FlowState loads its configuration from that file
    Then MCP server "legacy-vault" resolves to disabled
    And connecting the configured MCP servers makes no connection attempt to "legacy-vault"

  Scenario: An omitted enabled key still defaults the server to enabled
    Given a FlowState configuration file with MCP server "fresh-server" and no enabled key
    When FlowState loads its configuration from that file
    Then MCP server "fresh-server" resolves to enabled

  Scenario: The serve startup rebuild keeps exactly one live MCP connection per server
    Given a deferred-bootstrap FlowState application with MCP server "alpha" on a recording MCP client
    When a bootstrap-annotated command runs and rebuilds the application
    Then the pre-rebuild application's MCP connections are torn down
    And exactly one live connection to MCP server "alpha" remains
