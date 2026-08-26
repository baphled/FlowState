Feature: Voice capture
  As a developer building the talk-to-agents surface
  I want a command-driven audio capture tool
  So that recordings are produced as 16kHz mono s16le WAV files usable by whisper-cli

  Scenario: Capture command writes a WAV file at the requested duration
    Given a fake capture command that writes a 16kHz mono s16le WAV file
    When capture runs for 1 second
    Then the recording is a WAV file sampled at 16000 Hz, mono, 16-bit signed little-endian
    And the temporary recording is removed after transcription

  Scenario: Capture fails clearly when no capture binary is available
    Given no capture command is configured
    When capture runs for 1 second
    Then capture fails with an actionable error mentioning the missing binary
