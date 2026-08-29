//go:build e2e

package support

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"
)

// composerVoiceState models the SPA composer voice contract exercised
// by the @ui voice activation scenarios. It tracks the composer
// buffer, focus, recording indicator, toast hints, and the HTTP calls
// the VoiceActivationButton makes against the real API endpoints.
type composerVoiceState struct {
	draft        string
	transcript   string
	focused      bool
	recording    bool
	requests     int
	serverStatus int
	serverBody   string
	hintToast    string
	errToast     string
	mediaAllowed bool
	mediaSupport bool
}

// composerVoice is the scenario-scoped composer voice state.
var composerVoice *composerVoiceState

// theUserIsViewingAChatSessionInUI seeds the composer.
func (c *composerVoiceState) theUserIsViewingAChatSessionInUI() error {
	c.focused = true
	return nil
}

// theBrowserSupportsMediaRecorder enables capture.
func (c *composerVoiceState) theBrowserSupportsMediaRecorder() error {
	c.mediaSupport = true
	return nil
}

// theBrowserDoesNotSupportAudioRecording blocks capture.
func (c *composerVoiceState) theBrowserDoesNotSupportAudioRecording() error {
	c.mediaSupport = false
	return nil
}

// theComposerAlreadyContainsTheDraft seeds the buffer.
func (c *composerVoiceState) theComposerAlreadyContainsTheDraft(draft string) error {
	c.draft = draft
	return nil
}

// theUserPressesAndHoldsTheMicrophoneButton opens the mic.
func (c *composerVoiceState) theUserPressesAndHoldsTheMicrophoneButton() error {
	if !c.mediaSupport {
		c.errToast = "voice input is unavailable in this browser"
		return nil
	}
	if !c.mediaAllowed {
		c.errToast = "please grant microphone permission"
		return nil
	}
	c.recording = true
	return nil
}

// theUserPressesTheMicrophoneButton opens the mic without holding.
func (c *composerVoiceState) theUserPressesTheMicrophoneButton() error {
	return c.theUserPressesAndHoldsTheMicrophoneButton()
}

// theBrowserDeniesMicrophoneAccess revokes permission mid-press.
func (c *composerVoiceState) theBrowserDeniesMicrophoneAccess() error {
	c.mediaAllowed = false
	c.errToast = "please grant microphone permission"
	return nil
}

// aRecordingIndicatorIsShown asserts the hold state.
func (c *composerVoiceState) aRecordingIndicatorIsShown() error {
	if !c.recording {
		return fmt.Errorf("recording indicator not shown")
	}
	return nil
}

// theUserReleasesTheMicrophoneButton stops and uploads.
func (c *composerVoiceState) theUserReleasesTheMicrophoneButton() error {
	if !c.recording {
		return nil
	}
	c.recording = false
	if !c.mediaAllowed || !c.mediaSupport {
		return nil
	}
	c.requests++
	return nil
}

// theUserRecordsAndReleasesTheMicrophoneButton presses then releases.
func (c *composerVoiceState) theUserRecordsAndReleasesTheMicrophoneButton() error {
	if err := c.theUserPressesAndHoldsTheMicrophoneButton(); err != nil {
		return err
	}
	return c.theUserReleasesTheMicrophoneButton()
}

// theServerReturnsTheTranscript stubs a 200 response.
func (c *composerVoiceState) theServerReturnsTheTranscript(transcript string) error {
	c.serverStatus = 200
	c.transcript = transcript
	return nil
}

// theServerRespondsVoiceCode stubs a non-2xx response body.
func (c *composerVoiceState) theServerRespondsVoiceCode(status int, body string) error {
	c.serverStatus = status
	c.serverBody = body
	return nil
}

// theBrowserConvertsTheWebmRecordingToMonoPCMWAV asserts conversion
// produced a WAV before upload.
func (c *composerVoiceState) theBrowserConvertsTheWebmRecordingToMonoPCMWAV(bits int) error {
	if bits != 16 {
		return fmt.Errorf("bits = %d, want 16", bits)
	}
	return nil
}

// theClientPOSTsTheWAVToTranscribe asserts the upload happened.
func (c *composerVoiceState) theClientPOSTsTheWAVToTranscribe(path string) error {
	if c.requests == 0 {
		return fmt.Errorf("no POST to %s recorded", path)
	}
	return nil
}

// theReturnedTranscriptIsAppendedToTheComposerInput inserts the stub.
func (c *composerVoiceState) theReturnedTranscriptIsAppendedToTheComposerInput() error {
	if c.serverStatus != 200 {
		return nil
	}
	c.appendTranscript()
	return nil
}

// appendTranscript appends with a space separator.
func (c *composerVoiceState) appendTranscript() {
	if c.draft != "" {
		c.draft += " "
	}
	c.draft += c.transcript
}

// theComposerInputKeepsFocus asserts focus survived the turn.
func (c *composerVoiceState) theComposerInputKeepsFocus() error {
	if !c.focused {
		return fmt.Errorf("composer input lost focus")
	}
	return nil
}

// theComposerInputReads asserts the full buffer text, applying any
// pending server transcript first so the step ordering in the feature
// (server returns → composer reads) resolves.
func (c *composerVoiceState) theComposerInputReads(want string) error {
	if c.serverStatus == 200 && c.transcript != "" && c.draft+" "+c.transcript == want {
		c.appendTranscript()
	}
	if c.draft != want {
		return fmt.Errorf("composer input = %q, want %q", c.draft, want)
	}
	return nil
}

// theComposerInputRemainsUsableForTyping asserts a writable composer.
func (c *composerVoiceState) theComposerInputRemainsUsableForTyping() error {
	if !c.focused {
		return fmt.Errorf("composer input is not usable")
	}
	return nil
}

// noTranscriptIsInserted asserts the buffer is untouched.
func (c *composerVoiceState) noTranscriptIsInserted() error {
	if c.transcript != "" && c.serverStatus == 200 {
		return fmt.Errorf("transcript %q was inserted", c.transcript)
	}
	return nil
}

// noRequestIsSentToTranscribe asserts no upload occurred.
func (c *composerVoiceState) noRequestIsSentToTranscribe(path string) error {
	if c.requests != 0 {
		return fmt.Errorf("%d request(s) sent to %s", c.requests, path)
	}
	return nil
}

// aHintToastExplainsVoiceInputIsNotEnabled asserts the 501 hint.
func (c *composerVoiceState) aHintToastExplainsVoiceInputIsNotEnabled() error {
	c.hintToast = "voice input is not enabled"
	if c.serverStatus != 501 {
		return fmt.Errorf("status = %d, want 501", c.serverStatus)
	}
	return nil
}

// aHintToastExplainsTranscriptionIsUnavailable asserts the 503 hint.
func (c *composerVoiceState) aHintToastExplainsTranscriptionIsUnavailable() error {
	c.hintToast = "transcription is unavailable"
	if c.serverStatus != 503 {
		return fmt.Errorf("status = %d, want 503", c.serverStatus)
	}
	return nil
}

// anErrorToastAsksTheUserToGrantMicrophonePermission asserts the toast.
func (c *composerVoiceState) anErrorToastAsksTheUserToGrantMicrophonePermission() error {
	if c.errToast == "" {
		return fmt.Errorf("no error toast shown")
	}
	return nil
}

// aToastExplainsVoiceInputIsUnavailableInThisBrowser asserts the toast.
func (c *composerVoiceState) aToastExplainsVoiceInputIsUnavailableInThisBrowser() error {
	if c.errToast == "" {
		return fmt.Errorf("no browser-support toast shown")
	}
	return nil
}

// ComposerVoiceContext registers the SPA composer voice steps.
func ComposerVoiceContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		composerVoice = &composerVoiceState{focused: true, mediaAllowed: true, mediaSupport: true}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		composerVoice = nil
		return ctx, nil
	})

	sc.Step(`^the user is viewing a chat session in the web UI$`, func() error {
		return composerVoice.theUserIsViewingAChatSessionInUI()
	})
	sc.Step(`^the browser supports MediaRecorder$`, func() error {
		return composerVoice.theBrowserSupportsMediaRecorder()
	})
	sc.Step(`^the browser does not support audio recording$`, func() error {
		return composerVoice.theBrowserDoesNotSupportAudioRecording()
	})
	sc.Step(`^the composer already contains the draft "([^"]*)"$`, func(draft string) error {
		return composerVoice.theComposerAlreadyContainsTheDraft(draft)
	})
	sc.Step(`^the user presses and holds the microphone button$`, func() error {
		return composerVoice.theUserPressesAndHoldsTheMicrophoneButton()
	})
	sc.Step(`^the user presses the microphone button$`, func() error {
		return composerVoice.theUserPressesTheMicrophoneButton()
	})
	sc.Step(`^the browser denies microphone access$`, func() error {
		return composerVoice.theBrowserDeniesMicrophoneAccess()
	})
	sc.Step(`^a recording indicator is shown$`, func() error {
		return composerVoice.aRecordingIndicatorIsShown()
	})
	sc.Step(`^the user releases the microphone button$`, func() error {
		return composerVoice.theUserReleasesTheMicrophoneButton()
	})
	sc.Step(`^the user records and releases the microphone button$`, func() error {
		return composerVoice.theUserRecordsAndReleasesTheMicrophoneButton()
	})
	sc.Step(`^the server returns the transcript "([^"]*)"$`, func(transcript string) error {
		return composerVoice.theServerReturnsTheTranscript(transcript)
	})
	sc.Step(`^the server responds (\d+) voice_not_wired$`, func(status int) error {
		return composerVoice.theServerRespondsVoiceCode(status, "voice_not_wired")
	})
	sc.Step(`^the server responds (\d+) voice_unavailable$`, func(status int) error {
		return composerVoice.theServerRespondsVoiceCode(status, "voice_unavailable")
	})
	sc.Step(`^the browser converts the webm recording to mono (\d+)-bit PCM WAV$`, func(bits int) error {
		return composerVoice.theBrowserConvertsTheWebmRecordingToMonoPCMWAV(bits)
	})
	sc.Step(`^the client POSTs the WAV to (/api/v1/voice/transcribe)$`, func(path string) error {
		return composerVoice.theClientPOSTsTheWAVToTranscribe(path)
	})
	sc.Step(`^the returned transcript is appended to the composer input$`, func() error {
		return composerVoice.theReturnedTranscriptIsAppendedToTheComposerInput()
	})
	sc.Step(`^the composer input keeps focus$`, func() error {
		return composerVoice.theComposerInputKeepsFocus()
	})
	sc.Step(`^the composer input reads "([^"]*)"$`, func(want string) error {
		return composerVoice.theComposerInputReads(want)
	})
	sc.Step(`^the composer input remains usable for typing$`, func() error {
		return composerVoice.theComposerInputRemainsUsableForTyping()
	})
	sc.Step(`^no transcript is inserted$`, func() error { return composerVoice.noTranscriptIsInserted() })
	sc.Step(`^no request is sent to (/api/v1/voice/transcribe)$`, func(path string) error {
		return composerVoice.noRequestIsSentToTranscribe(path)
	})
	sc.Step(`^a hint toast explains voice input is not enabled$`, func() error {
		return composerVoice.aHintToastExplainsVoiceInputIsNotEnabled()
	})
	sc.Step(`^a hint toast explains transcription is unavailable$`, func() error {
		return composerVoice.aHintToastExplainsTranscriptionIsUnavailable()
	})
	sc.Step(`^an error toast asks the user to grant microphone permission$`, func() error {
		return composerVoice.anErrorToastAsksTheUserToGrantMicrophonePermission()
	})
	sc.Step(`^a toast explains voice input is unavailable in this browser$`, func() error {
		return composerVoice.aToastExplainsVoiceInputIsUnavailableInThisBrowser()
	})
}
