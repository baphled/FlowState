Feature: Voice STT
  As a developer building the talk-to-agents surface
  I want a whisper-cli wrapper
  So that recorded WAV files become transcripts without cloud services

  Scenario: Transcribe a WAV file via whisper-cli
    Given a fake STT command that emits "hello agent" on stdout
    When I transcribe a recorded WAV file
    Then the transcript is "hello agent"

  Scenario: Empty transcript is surfaced as an error
    Given a fake STT command that emits nothing on stdout
    When I transcribe a recorded WAV file
    Then transcription fails with an empty-transcript error

  Scenario: Missing STT binary fails with actionable guidance
    Given no STT command is configured
    When I transcribe a recorded WAV file
    Then transcription fails with an error mentioning whisper-cli
