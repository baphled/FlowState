Feature: Voice TTS synthesis for the frontend
  As a browser client of the FlowState HTTP API
  I want agent replies synthesised to WAV bytes via a TTS endpoint
  So that spoken output can play in the browser instead of on the host

  @voice @fe
  Scenario: Synthesising plain text returns WAV bytes
    Given a fake piper command that writes WAV bytes to stdout
    And a TTS tool configured with that command and model "en_GB-alan-medium"
    When I synthesise the text "hello there"
    Then synthesis returns WAV bytes

  @voice @fe
  Scenario: Synthesising strips markdown and expands symbols
    Given a fake piper command that writes WAV bytes to stdout
    And a TTS tool configured with that command and defaults
    When I synthesise the text "use `go test`:\n```go\nfmt.Println(1)\n```\nfoo -> bar"
    Then the synthesiser receives the sentence "use go test foo to bar"

  @voice @fe
  Scenario: Synthesising splits long replies into sentences
    Given a fake piper command that writes WAV bytes to stdout and logs its stdin
    And a TTS tool configured with that command and defaults
    When I synthesise the text "First sentence here. Second sentence follows!"
    Then the piper command is invoked once per sentence
    And the synthesised audio is the concatenation of per-sentence WAV bytes

  @voice @fe
  Scenario: Synthesis fails clearly when piper is unavailable
    Given a TTS command pointing at a nonexistent binary
    And a TTS tool configured with that command and defaults
    When I synthesise the text "hello"
    Then synthesis fails with an ErrTTSUnavailable error

  @voice @fe
  Scenario: Synthesis does not fall back to espeak-ng
    Given no piper binary on PATH but espeak-ng present
    When I synthesise the text "hello"
    Then synthesis fails with an ErrTTSUnavailable error

  @voice @fe
  Scenario: Piper command includes voice tuning flags
    Given a fake piper command that logs its argv
    And a TTS tool configured with that command and model "en_GB-alan-medium" and length_scale 1.2 and noise_scale 0.6 and sentence_silence 0.4
    When I synthesise the text "tuning check"
    Then the piper argv includes "--model en_GB-alan-medium"
    And the piper argv includes "--length_scale 1.2"
    And the piper argv includes "--noise_scale 0.6"
    And the piper argv includes "--sentence_silence 0.4"
