Feature: Voice session activation in serve
  As a FlowState operator running the SPA server
  I want the voice pipeline wired into the HTTP API when voice is enabled in config
  So that browser push-to-talk, runtime settings, and TTS work without CLI-only access

  @voice @wiring
  Scenario: Enabled voice config wires all voice endpoints
    Given a config with voice enabled
    When the serve command wires voice into the API server
    Then the voice turn pipeline is wired
    And the voice settings store is wired

  @voice @wiring
  Scenario: Disabled voice config leaves endpoints unwired for graceful degradation
    Given a config with voice disabled
    When the serve command wires voice into the API server
    Then the voice turn pipeline is not wired
    And the voice settings store is not wired

  @voice @wiring
  Scenario: The settings store is seeded from the loaded voice config
    Given a config with voice enabled and tts enabled
    When the serve command wires voice into the API server
    Then the voice settings report tts enabled
