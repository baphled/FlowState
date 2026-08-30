Feature: Voice conversation mode
  As a browser user of the FlowState SPA
  I want a conversation mode where my spoken turns are transcribed and sent
  to the chat session, with the agent reply spoken back via TTS
  So that I can talk with my agent hands-free instead of pushing to talk
  one turn at a time

  Background:
    Given a conversation service wired to a chat session

  @voice @conversation
  Scenario: Starting conversation mode reports active with the session
    When I start conversation mode for session "sess-conv-1"
    Then conversation mode is active
    And the conversation report carries session "sess-conv-1"

  @voice @conversation
  Scenario: A voice turn is transcribed and sent as a chat message
    Given a fake conversation STT command that emits "hello agent"
    And conversation mode is active for session "sess-conv-1"
    When I submit conversation audio to /api/v1/voice/conversation/turn
    Then the conversation response status is 200
    And the conversation response contains the transcript "hello agent"
    And the chat session receives the message "hello agent"

  @voice @conversation
  Scenario: The agent reply is spoken via TTS in conversation mode
    Given a fake conversation STT command that emits "hello agent"
    And conversation mode is active for session "sess-conv-1"
    And the chat session replies "all done, agent idle"
    And a synthesiser spy is wired
    When I submit conversation audio to /api/v1/voice/conversation/turn
    Then the conversation response status is 200
    And the synthesiser receives the text "all done, agent idle"

  @voice @conversation
  Scenario: TTS is skipped when speak_reply is false
    Given a fake conversation STT command that emits "hello agent"
    And conversation mode is active for session "sess-conv-1"
    And the chat session replies "all done, agent idle"
    And a synthesiser spy is wired
    When I submit conversation audio to /api/v1/voice/conversation/turn with speak_reply false
    Then the conversation response status is 200
    And the synthesiser receives no text

  @voice @conversation
  Scenario: A voice turn is rejected when conversation mode is not active
    Given a fake conversation STT command that emits "hello agent"
    When I submit conversation audio to /api/v1/voice/conversation/turn
    Then the conversation response status is 409

  @voice @conversation
  Scenario: Stopping conversation mode ends the session cleanly
    Given conversation mode is active for session "sess-conv-1"
    When I stop conversation mode
    Then conversation mode is not active
    And the conversation report carries session ""

  @voice @conversation
  Scenario: Voice disabled short-circuits conversation endpoints
    Given a server without a conversation service wired
    When I start conversation mode for session "sess-conv-1"
    Then the conversation response status is 501
