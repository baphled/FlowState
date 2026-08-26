Feature: Talk To Agents
  As a user
  I want my spoken words transcribed and handed to the existing dispatcher
  So that voice turns flow through the same session and mention machinery as typed turns

  @voice @b5
  Scenario: Transcript flows through existing dispatch
    Given a voice pipeline with a fake STT command emitting "hello agent"
    And a dispatcher spy is wired
    When the pipeline runs one talk turn
    Then the dispatcher receives content "hello agent"
    And the dispatcher receives ScanMentions true

  @voice @b5
  Scenario: Capture binary unavailable fails gracefully
    Given a voice pipeline with no capture binary
    And a dispatcher spy is wired
    When the pipeline runs one talk turn
    Then the pipeline returns ErrCaptureUnavailable
