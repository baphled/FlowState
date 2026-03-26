@planning @smoke
Feature: Deterministic Planning Loop
  The planning coordinator delegates to writer and reviewer agents
  in a structured loop to produce quality plans.

  Background:
    Given a planning coordinator agent is configured
    And the delegation table maps to writer and reviewer agents

  @delegation
  Scenario: Coordinator delegates to plan writer
    When the coordinator receives a planning request
    Then it should delegate to the plan-writer agent
    And a DelegationEvent with status "started" should be emitted
    And the delegation should complete with status "completed"

  @delegation
  Scenario: Coordinator delegates to plan reviewer after writing
    Given the plan-writer has generated a plan
    When the coordinator delegates to the plan-reviewer
    Then the reviewer should receive the plan via coordination store
    And a review verdict should be written to the coordination store

  @circuit-breaker
  Scenario: Circuit breaker opens after max rejections
    Given the reviewer has rejected 3 consecutive plans
    When the coordinator attempts another delegation
    Then the circuit breaker should be in open state
    And the coordinator should escalate to the user

  @coordination-store
  Scenario: Agents share context via coordination store
    Given a coordination store with chain ID "test-chain"
    When the coordinator writes requirements to the store
    And the writer reads requirements from the store
    Then the writer should receive the coordinator's requirements

  @visibility
  Scenario: Delegation events are visible to consumers
    When a delegation starts
    Then a DelegationEvent should contain the target agent name
    And the event should contain the model name
    And the event should contain a description