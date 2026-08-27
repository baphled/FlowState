Feature: TTS output endpoint
  As a browser client of the FlowState HTTP API
  I want agent reply text converted to audio over HTTP
  So that the browser can play spoken replies

  @voice @fe
  Scenario: POST /api/v1/voice/tts returns WAV audio
    Given an API server with a synthesiser that returns WAV bytes
    When I POST the text "hello browser" to /api/v1/voice/tts
    Then the response status is 200
    And the response content type is audio/wav
    And the response body is WAV audio

  @voice @fe
  Scenario: POST without text is rejected
    Given an API server with a synthesiser that returns WAV bytes
    When I POST no text to /api/v1/voice/tts
    Then the response status is 400

  @voice @fe
  Scenario: TTS endpoint without a synthesiser returns 501
    Given a server without a voice synthesiser wired
    When I POST the text "hello" to /api/v1/voice/tts
    Then the response status is 501

  @voice @fe
  Scenario: Synthesiser failure surfaces as 503
    Given an API server with a synthesiser that fails
    When I POST the text "hello" to /api/v1/voice/tts
    Then the response status is 503
