Feature: Frontend voice settings API
  As a browser client of the FlowState HTTP API
  I want runtime voice settings exposed over HTTP
  So that the UI can toggle and tune voice behaviour without editing YAML

  @voice @fe
  Scenario: GET returns the current voice settings
    Given a voice settings store with tts_enabled true and tts_model "en_GB-alan-medium" and length_scale 1.1 and noise_scale 0.7 and sentence_silence 0.3
    When I GET /api/v1/voice/settings
    Then the response status is 200
    And the settings JSON shows enabled true and tts_model "en_GB-alan-medium" and length_scale 1.1 and noise_scale 0.7 and sentence_silence 0.3

  @voice @fe
  Scenario: PATCH updates runtime voice settings
    Given a voice settings store with defaults
    When I PATCH /api/v1/voice/settings with tts_enabled true and tts_model "en_GB-alan-medium"
    Then the response status is 200
    And the settings JSON shows enabled true and tts_model "en_GB-alan-medium"

  @voice @fe
  Scenario: PATCH with invalid payload is rejected
    Given a voice settings store with defaults
    When I PATCH /api/v1/voice/settings with length_scale -3
    Then the response status is 400

  @voice @fe
  Scenario: Settings endpoints without a store return 501
    Given a server without voice settings wired
    When I GET /api/v1/voice/settings
    Then the response status is 501
