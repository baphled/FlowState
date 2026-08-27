Feature: Voice transcription API
  As a client of the FlowState HTTP API
  I want to upload a recorded WAV and receive its transcript
  So that voice turns can originate outside the CLI while reusing the same local STT tool

  @voice @b6
  Scenario: Transcribing an uploaded WAV returns the transcript
    Given a fake STT command that emits "hello from the api"
    When I POST a WAV file to /api/v1/voice/transcribe
    Then the response status is 200
    And the response contains the transcript "hello from the api"

  @voice @b6
  Scenario: Request without audio is rejected
    When I POST no audio to /api/v1/voice/transcribe
    Then the response status is 400

  @voice @b6
  Scenario: Missing STT binary surfaces a 503 with guidance
    Given no STT command is configured
    When I POST a WAV file to /api/v1/voice/transcribe
    Then the response status is 503
