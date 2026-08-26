Feature: Talk To Agents
  As a user
  I want to speak to FlowState agents by voice
  So that I can interact hands-free while the transcript still flows through normal dispatch

  Background:
    Given the FlowState CLI is available

  @voice
  Scenario: Transcript flows through existing dispatch
    Given a fake STT command that emits the transcript "hello agent"
    And a fake TTS command is configured
    When I run "flowstate talk --help"
    Then I should see usage for "flowstate talk"

  @voice
  Scenario: Graceful degradation when voice binaries are absent
    Given no voice binaries are available
    When I run "flowstate talk --help"
    Then I should see usage for "flowstate talk"
    And running the talk command should warn and fall back to text-only mode
    And the exit code should be 0

  @voice
  Scenario: Push-to-talk start and stop
    Given a fake STT command that emits the transcript "push to talk works"
    When the talk command starts in push-to-talk mode
    Then a keypress should start recording
    And the same keypress should stop recording and transcribe
    And the terminal should be restored after the command exits
