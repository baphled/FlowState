@swarm @adulting @memory
Feature: Adulting swarm memory and knowledge base access
  As the adulting swarm orchestrator
  I want lead and specialist agents to read from memory and the knowledge base
  So that triage and analysis draw on historical financial data and deadlines

  Background:
    Given the adulting swarm is defined
    And the adulting agent manifests are loaded

  Scenario: Lead agent opts in to recall
    Given the "life-admin-lead" agent manifest
    Then it should have "uses_recall" set to "true"

  Scenario: Bill tracker opts in to recall
    Given the "bill-tracker" agent manifest
    Then it should have "uses_recall" set to "true"

  Scenario: Deadline scanner opts in to recall
    Given the "deadline-scanner" agent manifest
    Then it should have "uses_recall" set to "true"

  Scenario: Letter drafter does not use recall
    Given the "letter-drafter" agent manifest
    Then it should have "uses_recall" set to "false"

  Scenario: Lead agent has memory and vault tools
    Given the "life-admin-lead" agent manifest
    Then its capabilities.tools should include "mcp_memory_search_nodes"
    And its capabilities.tools should include "mcp_memory_open_nodes"
    And its capabilities.tools should include "mcp_vault-rag_query_vault"

  Scenario: Bill tracker has memory and vault tools
    Given the "bill-tracker" agent manifest
    Then its capabilities.tools should include "mcp_memory_search_nodes"
    And its capabilities.tools should include "mcp_memory_open_nodes"
    And its capabilities.tools should include "mcp_vault-rag_query_vault"

  Scenario: Deadline scanner has memory and vault tools
    Given the "deadline-scanner" agent manifest
    Then its capabilities.tools should include "mcp_memory_search_nodes"
    And its capabilities.tools should include "mcp_memory_open_nodes"
    And its capabilities.tools should include "mcp_vault-rag_query_vault"

  Scenario: Letter drafter does not have memory or vault tools
    Given the "letter-drafter" agent manifest
    Then its capabilities.tools should not include "mcp_memory_search_nodes"
    And its capabilities.tools should not include "mcp_vault-rag_query_vault"

  Scenario: Lead agent has memory-keeper and knowledge-base in always_active_skills
    Given the "life-admin-lead" agent manifest
    Then its always_active_skills should include "memory-keeper"
    And its always_active_skills should include "knowledge-base"

  Scenario: Bill tracker has memory-keeper and knowledge-base in always_active_skills
    Given the "bill-tracker" agent manifest
    Then its always_active_skills should include "memory-keeper"
    And its always_active_skills should include "knowledge-base"

  Scenario: Deadline scanner has memory-keeper and knowledge-base in always_active_skills
    Given the "deadline-scanner" agent manifest
    Then its always_active_skills should include "memory-keeper"
    And its always_active_skills should include "knowledge-base"

  Scenario: Letter drafter does not have memory or KB skills
    Given the "letter-drafter" agent manifest
    Then its always_active_skills should not include "memory-keeper"
    And its always_active_skills should not include "knowledge-base"

  Scenario: All adulting agents with always_active_skills declare skill_load
    Given the "life-admin-lead" agent manifest
    Then its capabilities.tools should include "skill_load"

  Scenario: Recall-enabled agents can invoke memory search via skill_load
    Given the "life-admin-lead" agent manifest
    Then its capabilities.tools should include "skill_load"
    And its always_active_skills should include "memory-keeper"
    And its capabilities.tools should include "mcp_memory_search_nodes"
