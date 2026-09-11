Feature: Standard Prometheus runtime collectors
  As an operator monitoring FlowState
  I want Go runtime and process metrics exported alongside the existing gauges
  So that I can diagnose memory, GC, and process health from /metrics

  Background:
    Given FlowState is running

  @metrics
  Scenario: /metrics exposes Go runtime collectors
    When I scrape the Prometheus metrics endpoint
    Then the exposition contains "go_goroutines"
    And the exposition contains "go_memstats_alloc_bytes"
    And the exposition contains "process_resident_memory_bytes"

  @metrics
  Scenario: existing FlowState gauges remain exposed
    When I scrape the Prometheus metrics endpoint
    Then the exposition contains a flowstate_ metric family
