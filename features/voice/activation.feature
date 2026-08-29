Feature: SPA voice activation
  As a browser user of the FlowState SPA
  I want a push-to-talk microphone button next to the chat input
  So that I can dictate a chat turn without typing

  @voice @spa
  Scenario: Push-to-talk records WAV and inserts the transcript
    Given a fake STT command that emits "hello from the spa"
    And an API server with the voice turn pipeline wired
    And a push-to-talk control bound to the chat input
    When I hold the push-to-talk button and record 1 second of WAV audio
    And I release the push-to-talk button
    Then the WAV audio is posted to /api/v1/voice/transcribe
    And the transcript "hello from the spa" is inserted into the chat input

  @voice @spa
  Scenario: Push-to-talk is disabled when voice is off
    Given a voice settings store with defaults
    And a push-to-talk control bound to the chat input
    When I GET /api/v1/voice/settings
    Then the settings JSON shows enabled false
    And the push-to-talk button is disabled

  @voice @spa
  Scenario: Push-to-talk is enabled when voice is on
    Given a voice settings store with tts_enabled true and tts_model "en_GB-alan-medium" and length_scale 1.1 and noise_scale 0.7 and sentence_silence 0.3
    And a push-to-talk control bound to the chat input
    When I GET /api/v1/voice/settings
    Then the settings JSON shows enabled true and tts_model "en_GB-alan-medium" and length_scale 1.1 and noise_scale 0.7 and sentence_silence 0.3
    And the push-to-talk button is enabled
