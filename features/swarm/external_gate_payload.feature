@swarm @gate-payload
Feature: External gate payload schema
  As a swarm operator debugging a post-member gate
  I want the gate executable to receive the payload in the documented JSON shape
  So that gate failures point at agent output rather than host-side encoding drift

  Scenario: JSON object payloads are forwarded as objects
    Given an external gate executable that requires a task_plan field
    When FlowState invokes the external gate with this payload:
      """
      {"task_plan":"kubernetes autoscaling","research":"kubernetes autoscaling notes"}
      """
    Then the external gate should pass
