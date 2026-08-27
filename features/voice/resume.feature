Feature: Talk session resume and mention passthrough
  As a voice user
  I want spoken turns to resume an existing session and route @-mentions
  So that voice is a first-class entry into the same dispatch machinery as typing

  @voice @f2
  Scenario: Session id resumes the existing session
    Given a fake STT command that emits the transcript "continue where we left off"
    And a dispatcher spy is wired
    When the talk command dispatches with session "sess-123"
    Then the dispatch request carries session "sess-123"

  @voice @f2
  Scenario: Transcript with an @-mention routes via mention scanning
    Given a fake STT command that emits the transcript "@dev-swarm ship the release"
    And a dispatcher spy is wired
    When the talk command dispatches one turn
    Then the dispatch request carries ScanMentions true
