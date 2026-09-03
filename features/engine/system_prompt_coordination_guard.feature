@promptguard
Feature: Coordination-store file guard in system prompt assembly

  The coordination store is a single flat JSON file (`coordination.json`,
  by default `~/.local/share/flowstate/coordination.json`). The backing
  FileStore loads once and rewrites the whole file per mutation, so any
  write made directly to the file via the read/write/edit tools, `bash`,
  `cat`, or shell redirection is invisible to the running process and is
  silently overwritten on its next persist.

  The system-prompt assembly pipeline therefore injects an explicit,
  unambiguous prohibition. Manifests carrying the `coordination_store`
  tool receive the Tool-Usage Requirement including the full
  file-access prohibition, and every manifest — with or without the
  tool — receives an unconditional blanket guard line.

  Scenario: Manifest with coordination_store receives the explicit prohibition
    Given an agent manifest with capabilities tools "read,write,coordination_store"
    When the system prompt is built
    Then the system prompt should contain "coordination.json"
    And the system prompt should contain "NEVER read"
    And the system prompt should contain "hard prohibition with no exceptions"

  Scenario: Manifest without coordination_store still receives the blanket guard
    Given an agent manifest with capabilities tools "read,write"
    When the system prompt is built
    Then the system prompt should contain "NEVER read or write `coordination.json`"
    And the system prompt should contain "sanctioned tools and CLI verbs"
