Feature: Skill Load Tool
  As an agent
  I want to load skill content by name
  So that I can access skill documentation and instructions

  @skill
  Scenario: Load valid skill by name
    Given the skill_load tool is available
    When I call skill_load with skill name "golang"
    Then the tool should return the skill content
    And the content should contain skill documentation

  @skill
  Scenario: Load non-existent skill returns error
    Given the skill_load tool is available
    When I call skill_load with skill name "nonexistent-skill-xyz"
    Then the tool should return an error
    And the error message should indicate skill not found
