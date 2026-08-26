Feature: Voice TTS
  As a user who has opted in to spoken responses
  I want agent replies spoken through a local TTS binary
  So that I can keep working hands-free after talking to my agents

  @voice @b3
  Scenario: Spoken reply when TTS is enabled
    Given a fake TTS command that writes spoken output
    And a TTS tool configured with that command
    When I speak the reply "all done, agent idle"
    Then the TTS command receives the reply text

  @voice @b3
  Scenario: TTS stays silent when not opted in
    Given no TTS command is configured
    When I speak the reply "all done, agent idle"
    Then no TTS command is invoked

  @voice @b3
  Scenario: Missing TTS binary degrades to silent output
    Given a TTS command pointing at a nonexistent binary
    And a TTS tool configured with that command
    When I speak the reply "all done, agent idle"
    Then speaking fails with an ErrTTSUnavailable error
