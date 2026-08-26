@mcp
Feature: MCP Tool Integration
  Background:
    Given FlowState is configured with an MCP server

  @smoke
  Scenario: Discover tools from MCP server
    When I connect to the MCP server
    Then I should see available tools from the server

  @smoke
  Scenario: Execute MCP tool and get result
    Given I am connected to the MCP server
    When I ask the agent to use an MCP tool
    Then the tool should execute and return a result

  @smoke
  Scenario: MCP server connection lifecycle
    When I connect to the MCP server
    And I disconnect from the MCP server
    Then the server connection should be cleaned up
