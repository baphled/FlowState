@swarm @fs-pollution-guard
Feature: Filesystem pollution guard
  As the swarm orchestrator
  I want coordination and scratch data kept out of the filesystem
  So that inter-agent data flows exclusively through the coordination store

  Background:
    Given the fs-pollution-guard gate runner is registered

  Scenario: Filesystem writes of coordination data are rejected
    When a member reports a write to "/tmp/chain/handoff.json" with content type "coordination"
    Then the fs-pollution-guard gate should fail
    And the failure should name the offending path "/tmp/chain/handoff.json"

  Scenario: Scratch notes written outside sanctioned paths are rejected
    When a member reports a write to "scratch-notes.md" with content type "scratch"
    Then the fs-pollution-guard gate should fail

  Scenario: Sanctioned worktree source writes pass
    When a member reports a write to "internal/swarm/gates.go" with content type "source"
    Then the fs-pollution-guard gate should pass

  Scenario: Sanctioned vault writes for the KB-Curator pass
    When a member with role "kb-curator" reports a write to "/home/baphled/vaults/baphled/notes.md" with content type "vault"
    Then the fs-pollution-guard gate should pass

  Scenario: Pre-commit blocks untracked pollution files
    Given an untracked file "stray-report.json" exists in the repo
    Then the pre-commit pollution check should report it as blocked
