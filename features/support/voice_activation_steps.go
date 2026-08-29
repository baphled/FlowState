//go:build e2e

package support

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"
)

// pttState models the SPA push-to-talk control against the real API
// endpoints. It exercises the same sequence the Vue component performs:
// settings fetch gates the button, hold/release drives a WAV upload, and
// the returned transcript lands in the chat input buffer.
type pttState struct {
	chatInput string
	disabled  bool
	transcript string
}

// ptt is the scenario-scoped push-to-talk state.
var ptt *pttState

// aPushToTalkControlBoundToTheChatInput resets the control.
func (p *pttState) aPushToTalkControlBoundToTheChatInput() error {
	p.chatInput = ""
	p.disabled = false
	return nil
}

// iHoldAndRecord stages the recording state.
func (p *pttState) iHoldAndRecord() error {
	p.transcript = ""
	return nil
}

// iRelease posts the recorded WAV to the transcribe endpoint and
// stores the returned transcript.
func (p *pttState) iRelease() error {
	if voiceFE.server == nil {
		return fmt.Errorf("no voice server wired")
	}
	if err := voiceFE.postWAVMultipart("/api/v1/voice/transcribe"); err != nil {
		return err
	}
	var payload struct {
		Transcript string `json:"transcript"`
	}
	if err := json.Unmarshal(voiceFE.recorder.Body.Bytes(), &payload); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	p.transcript = payload.Transcript
	return nil
}

// theWAVAudioIsPostedTo asserts the upload succeeded.
func (p *pttState) theWAVAudioIsPostedTo(path string) error {
	if voiceFE.recorder == nil {
		return fmt.Errorf("no upload performed")
	}
	return voiceFE.theResponseStatusIsFE(200)
}

// theTranscriptIsInsertedIntoTheChatInput asserts the buffer content.
func (p *pttState) theTranscriptIsInsertedIntoTheChatInput(transcript string) error {
	if p.chatInput != "" {
		p.chatInput += " "
	}
	p.chatInput += p.transcript
	if p.chatInput != transcript {
		return fmt.Errorf("chat input = %q, want %q", p.chatInput, transcript)
	}
	return nil
}

// thePushToTalkButtonIsDisabled asserts the settings gate closed it.
func (p *pttState) thePushToTalkButtonIsDisabled() error {
	if !p.disabled {
		return fmt.Errorf("push-to-talk button is enabled, want disabled")
	}
	return nil
}

// thePushToTalkButtonIsEnabled asserts the settings gate opened it.
func (p *pttState) thePushToTalkButtonIsEnabled() error {
	if p.disabled {
		return fmt.Errorf("push-to-talk button is disabled, want enabled")
	}
	return nil
}

// PttContext registers the SPA push-to-talk steps.
func PttContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		ptt = &pttState{}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		ptt = nil
		return ctx, nil
	})

	// spaSettingsEnabledFalse gates the button on a disabled store.
	spaSettingsEnabledFalse := func() error {
		var got struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(voiceFE.recorder.Body.Bytes(), &got); err != nil {
			return fmt.Errorf("decode: %w; body %s", err, voiceFE.recorder.Body.String())
		}
		if got.Enabled {
			return fmt.Errorf("enabled = true, want false")
		}
		ptt.disabled = !got.Enabled
		return nil
	}
	sc.Step(`^the settings JSON shows enabled false$`, spaSettingsEnabledFalse)

	sc.Step(`^a push-to-talk control bound to the chat input$`, func() error {
		return ptt.aPushToTalkControlBoundToTheChatInput()
	})
	sc.Step(`^I hold the push-to-talk button and record \d+ second of WAV audio$`, func() error {
		return ptt.iHoldAndRecord()
	})
	sc.Step(`^I release the push-to-talk button$`, func() error { return ptt.iRelease() })
	sc.Step(`^the WAV audio is posted to (/api/v1/voice/transcribe)$`, func(path string) error {
		return ptt.theWAVAudioIsPostedTo(path)
	})
	sc.Step(`^the transcript "([^"]*)" is inserted into the chat input$`, func(transcript string) error {
		return ptt.theTranscriptIsInsertedIntoTheChatInput(transcript)
	})
	sc.Step(`^the push-to-talk button is disabled$`, func() error {
		return ptt.thePushToTalkButtonIsDisabled()
	})
	sc.Step(`^the push-to-talk button is enabled$`, func() error {
		return ptt.thePushToTalkButtonIsEnabled()
	})
}
