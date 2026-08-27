Feature: Frontend voice turns
  As a browser client of the FlowState HTTP API
  I want to upload recorded audio and have it transcribed and dispatched
  So that a voice turn can originate from the browser microphone

  @voice @fe
  Scenario: Uploaded WAV runs STT then dispatch with mention scanning
    Given a fake STT command that emits "hello @swarm from the browser"
    And an API server with the voice turn pipeline wired
    When I POST a WAV file to /api/v1/voice/transcribe
    Then the response status is 200
    And the response contains the transcript "hello @swarm from the browser"
    And the dispatcher receives the transcript with ScanMentions true

  @voice @fe
  Scenario: Raw-body WAV upload without multipart is accepted
    Given a fake STT command that emits "raw body works"
    And an API server with the voice turn pipeline wired
    When I POST raw WAV bytes to /api/v1/voice/transcribe
    Then the response status is 200
    And the response contains the transcript "raw body works"

  @voice @fe
  Scenario: Browser webm uploads are rejected with a clear message
    Given an API server with the voice turn pipeline wired
    When I POST webm bytes to /api/v1/voice/transcribe
    Then the response status is 400
    And the error message mentions WAV
