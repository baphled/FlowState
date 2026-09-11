Feature: Flag-gated pprof instrumentation
  As an operator diagnosing FlowState performance
  I want the Go runtime profiler served behind an explicit flag
  So that profiling endpoints are not exposed unless I ask for them

  Background:
    Given FlowState is running

  Scenario: pprof is disabled by default
    When the serve command starts with no pprof flag
    Then no pprof server is listening

  Scenario: pprof serves on the flagged address
    When the serve command starts with pprof address "localhost:0"
    Then the pprof server is listening
    And "GET /debug/pprof/" returns a profile index

  Scenario: pprof refuses a non-loopback address
    When the serve command starts with pprof address "0.0.0.0:6060"
    Then serve startup fails with a non-loopback pprof address error
