@voice
@slow
Feature: Voice conversation served from flowstate serve
  As a FlowState operator running the daemon
  I want the conversational voice loop served entirely from `flowstate serve`
  So that voice works through the daemon's API surface with no standalone CLI command

  Background:
    Given the serve command wires voice into the API server with a chat dispatcher

  @voice @voice_serve
  Scenario: Enabled voice config wires the conversation endpoints
    Then the conversation mode endpoints are wired

  @voice @voice_serve
  Scenario: A served voice turn round-trips transcript and reply
    Given a fake serve STT command that emits "hello agent"
    And serve conversation mode is active for session "sess-serve-1"
    When I submit serve conversation audio to /api/v1/voice/conversation/turn
    And the serve chat session receives the message "hello agent"

  @voice @voice_serve
  Scenario: The agent reply is spoken via TTS in conversation mode
    Given a fake serve STT command that emits "hello agent"
    And serve conversation mode is active for session "sess-serve-1"
    And the serve chat session replies "all done, agent idle"
    When I submit serve conversation audio to /api/v1/voice/conversation/turn
    And the serve synthesiser receives the text "all done, agent idle"

  @voice @voice_serve
  Scenario: A served voice turn with an empty transcript returns 422 not 500
    Given a serve STT command that emits nothing
    And serve conversation mode is active for session "sess-serve-1"
    When I submit serve conversation audio to /api/v1/voice/conversation/turn
    Then the serve turn response status is 422 with code "empty_transcript"

  @voice @voice_serve
  Scenario: A served voice turn with a failing STT command returns 502
    Given a serve STT command that exits non-zero
    And serve conversation mode is active for session "sess-serve-1"
    When I submit serve conversation audio to /api/v1/voice/conversation/turn
    Then the serve turn response status is 502 with code "stt_failed"

  @voice @voice_serve
  Scenario: A served voice turn prefers the whisper stderr transcript
    Given a serve STT command that emits "hello stderr agent" on stderr
    And serve conversation mode is active for session "sess-serve-1"
    When I submit serve conversation audio to /api/v1/voice/conversation/turn
    And the serve chat session receives the message "hello stderr agent"

  @voice @voice_serve
  Scenario: Disabled voice config leaves conversation endpoints unwired
    Given a serve config with voice disabled
    And the serve command wires serve voice from the config
    Then the conversation mode endpoints are not wired

  @voice @voice_serve
  Scenario: The talk CLI command is removed
    Then the flowstate talk command does not exist
