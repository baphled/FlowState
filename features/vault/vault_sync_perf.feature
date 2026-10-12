Feature: Vault sync performance
  As an operator running vault sync against a local Ollama embedder
  I want sync to use bounded concurrency, batched embeds, and deferred Qdrant writes
  So that my machine stays usable during sync and a stalled embedder aborts instead of hanging forever

  Background:
    Given a sync perf vault directory with markdown files

  @vault @perf
  Scenario: Sync completes with bounded concurrency and does not exceed the worker limit
    When I run a vault sync with at most 2 concurrent file workers over 6 changed files
    Then the sync should complete successfully
    And the observed peak concurrent file workers should not exceed 2

  @vault @perf
  Scenario: Sync uses one batched embed request per batch window
    When I run a vault sync with a batch-capable embedder and 8 chunks at batch size 4
    Then the sync should complete successfully
    And the embedder should have received 2 batch requests
    And the embedder should have received 0 single-chunk requests

  @vault @perf
  Scenario: Sync falls back to single embeds when the embedder rejects batching
    When I run a vault sync with a batch-rejecting embedder
    Then the sync should complete successfully
    And the embedder should have received only single-chunk requests

  @vault @perf
  Scenario: State sidecar is written once per sync pass instead of per file
    When I run a vault sync over 10 changed files with checkpoint interval 50
    Then the sidecar should have been saved exactly once

  @vault @perf
  Scenario: Qdrant upserts are deferred and flushed once at the end of sync
    When I run a vault sync over 5 changed files with a flush-capable store
    Then every per-file upsert should have been issued with wait=false
    And the store should have flushed exactly once after the last upsert

  @vault @perf
  Scenario: Sync aborts when the embedder stalls beyond the HTTP timeout
    When I run a vault sync against an embedder that accepts connections but never responds
    Then the sync should abort within a bounded timeout
