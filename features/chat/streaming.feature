Feature: Streaming Responses
  As a user
  I want to see responses stream in real-time
  So that I can see progress while the AI generates its response

  Background:
    Given FlowState is running

  @smoke
  Scenario: Streaming tokens appear incrementally
    Given I am in insert mode
    When I type "Tell me a short story"
    And I press Enter
    Then I should see tokens appearing
    And I should see a complete response

  @smoke
  Scenario: Streaming can be interrupted
    Given I am in insert mode
    When I type "Write a very long essay"
    And I press Enter
    And I should see tokens appearing
