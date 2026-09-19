@engine
Feature: Skills guarantee

  Invariant: when a manifest declares always-active skills, the skill
  content must actually reach the provider on the first turn, and a
  manifest that names a skill missing on disk must be surfaced loudly —
  a silent drop means the agent runs without instructions nobody
  noticed were lost.

  These scenarios are written tests-first against the current engine.
  The second scenario documents a violation and is expected to FAIL.
  See docs/invariants/TURN_LIFECYCLE_INVARIANTS.md.

  Scenario: Always-active skill content is present in the first turn's provider request
    Given an engine carries one always-active skill
    When the always-active turn is streamed once
    Then the first provider request embeds the always-active skill body

  Scenario: A manifest listing a skill missing on disk fails or warns at validation
    Given a manifest lists an always-active skill that is missing on disk
    Then the missing skill is reported by the loader or validator
