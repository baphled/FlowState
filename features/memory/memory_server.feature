Feature: Memory Server Knowledge Graph
  As a user or agent
  I want to manage entities and relationships in a memory server
  So that I can persist, retrieve, and query knowledge across sessions

  @memory
  Scenario: Create a new entity and retrieve it
    Given the memory server is running
    When I create an entity named "ProjectX" with description "A secret project"
    Then I should be able to retrieve the entity "ProjectX"
    And the entity details should include "A secret project"

  @memory
  Scenario: Search for entities by query
    Given the memory server contains entities "Alpha", "Beta", and "Gamma"
    When I search for entities with the query "Al"
    Then I should see "Alpha" in the search results
    And I should not see "Beta" or "Gamma" in the search results

  @memory
  Scenario: Add observations to an existing entity
    Given the entity "ProjectX" exists in the memory server
    When I add the observation "First milestone reached" to "ProjectX"
    Then the entity "ProjectX" should include the observation "First milestone reached"

  @memory
  Scenario: Delete an entity and its relations
    Given the entity "ProjectX" exists and is related to "Alpha"
    When I delete the entity "ProjectX"
    Then the entity "ProjectX" should no longer exist in the memory server
    And any relations involving "ProjectX" should be removed

  @memory
  Scenario: Handle request for non-existent entity gracefully
    Given the memory server is running
    When I attempt to retrieve the entity "NonExistentEntity"
    Then I should receive a not found error message

  @memory
  Scenario: Persist data across server restarts
    Given the entity "PersistentEntity" exists in the memory server
    When I restart the memory server
    Then the entity "PersistentEntity" should still exist after restart
