@voice @ui
Feature: Voice push-to-talk activation in the web composer
  The flowstate-web chat composer offers a hold-to-talk microphone
  button. Holding it captures browser audio; releasing converts the
  recording to WAV client-side, POSTs it to the voice transcribe API,
  and inserts the returned transcript into the composer input. When
  the server has no voice pipeline the composer stays fully usable.

  Background:
    Given the user is viewing a chat session in the web UI
      And the browser supports MediaRecorder

  Scenario: Hold-to-talk records and inserts the transcript
    When the user presses and holds the microphone button
    Then a recording indicator is shown
    When the user releases the microphone button
    Then the browser converts the webm recording to mono 16-bit PCM WAV
      And the client POSTs the WAV to /api/v1/voice/transcribe
      And the returned transcript is appended to the composer input
      And the composer input keeps focus

  Scenario: Appending to an existing draft
    Given the composer already contains the draft "status update:"
    When the user records and releases the microphone button
      And the server returns the transcript "all tests green"
    Then the composer input reads "status update: all tests green"

  Scenario: Voice not wired returns a non-blocking hint
    When the user records and releases the microphone button
      And the server responds 501 voice_not_wired
    Then a hint toast explains voice input is not enabled
      And the composer input remains usable for typing
      And no transcript is inserted

  Scenario: Whisper unavailable returns a non-blocking hint
    When the user records and releases the microphone button
      And the server responds 503 voice_unavailable
    Then a hint toast explains transcription is unavailable
      And the composer input remains usable for typing

  Scenario: Microphone permission denied
    When the user presses the microphone button
      And the browser denies microphone access
    Then an error toast asks the user to grant microphone permission
      And no request is sent to /api/v1/voice/transcribe

  Scenario: Browser without MediaRecorder support
    Given the browser does not support audio recording
    When the user presses the microphone button
    Then a toast explains voice input is unavailable in this browser
