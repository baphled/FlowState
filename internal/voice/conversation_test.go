package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// convSpyDispatcher records dispatched transcripts and returns a
// canned reply.
type convSpyDispatcher struct {
	session     string
	transcripts []string
	reply       string
	err         error
}

// ConversationReply implements ConversationTurnDispatcher.
func (d *convSpyDispatcher) ConversationReply(_ context.Context, session, transcript string) (string, error) {
	d.session = session
	d.transcripts = append(d.transcripts, transcript)
	return d.reply, d.err
}

// convSpySynth records synthesised texts.
type convSpySynth struct {
	texts []string
	wav   []byte
	err   error
}

// Synthesize implements the synthesiser interface.
func (s *convSpySynth) Synthesize(_ context.Context, text string) ([]byte, error) {
	s.texts = append(s.texts, text)
	return s.wav, s.err
}

// fakeConvSTT writes an STT fake binary emitting transcript and
// points FLOWSTATE_VOICE_STT at it.
func fakeConvSTT(t *testing.T, transcript string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "stt-fake")
	script := "#!/bin/sh\nprintf '%s'\n"
	script = fmt.Sprintf(script, transcript)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stt fake: %v", err)
	}
	t.Setenv("FLOWSTATE_VOICE_STT", bin)
}

func TestConversationStartStopActive(t *testing.T) {
	svc := NewConversationService("", &convSpyDispatcher{}, nil)
	if got := svc.Active(); got != "" {
		t.Fatalf("Active() = %q, want empty", got)
	}
	svc.Start("sess-1")
	if got := svc.Active(); got != "sess-1" {
		t.Fatalf("Active() = %q, want sess-1", got)
	}
	svc.Stop()
	if got := svc.Active(); got != "" {
		t.Fatalf("Active() after Stop = %q, want empty", got)
	}
}

func TestConversationTurnInactive(t *testing.T) {
	svc := NewConversationService("", &convSpyDispatcher{}, nil)
	_, err := svc.Turn(context.Background(), ConversationTurn{Audio: []byte("RIFFxxxxWAVE")})
	if !errors.Is(err, ErrConversationInactive) {
		t.Fatalf("Turn err = %v, want ErrConversationInactive", err)
	}
}

func TestConversationTurnEmptyAudio(t *testing.T) {
	svc := NewConversationService("", &convSpyDispatcher{}, nil)
	svc.Start("sess-1")
	_, err := svc.Turn(context.Background(), ConversationTurn{})
	if err == nil || errors.Is(err, ErrConversationInactive) {
		t.Fatalf("Turn err = %v, want empty-audio error", err)
	}
}

func TestConversationTurnTranscribesAndSpeaks(t *testing.T) {
	fakeConvSTT(t, "hello agent")
	d := &convSpyDispatcher{reply: "all done"}
	synth := &convSpySynth{wav: []byte("RIFFwav")}
	svc := NewConversationService("", d, synth)
	svc.Start("sess-1")
	res, err := svc.Turn(context.Background(), ConversationTurn{Audio: []byte("RIFFb\x00\x00\x00WAVEfmt "), SpeakReply: true})
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Transcript != "hello agent" {
		t.Errorf("transcript = %q, want hello agent", res.Transcript)
	}
	if res.Reply != "all done" {
		t.Errorf("reply = %q, want all done", res.Reply)
	}
	if string(res.Audio) != "RIFFwav" {
		t.Errorf("audio = %q, want RIFFwav", res.Audio)
	}
	if d.session != "sess-1" || len(d.transcripts) != 1 || d.transcripts[0] != "hello agent" {
		t.Errorf("dispatcher saw session=%q transcripts=%v", d.session, d.transcripts)
	}
	if len(synth.texts) != 1 || synth.texts[0] != "all done" {
		t.Errorf("synth texts = %v, want [all done]", synth.texts)
	}
}

func TestConversationTurnSkipsTTSWhenNotRequested(t *testing.T) {
	fakeConvSTT(t, "quiet turn")
	d := &convSpyDispatcher{reply: "hush"}
	synth := &convSpySynth{}
	svc := NewConversationService("", d, synth)
	svc.Start("sess-2")
	res, err := svc.Turn(context.Background(), ConversationTurn{Audio: []byte("RIFFb\x00\x00\x00WAVEfmt ")})
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Audio != nil {
		t.Errorf("audio = %q, want nil", res.Audio)
	}
	if len(synth.texts) != 0 {
		t.Errorf("synth texts = %v, want none", synth.texts)
	}
}

func TestConversationTurnTTSFailureDoesNotFailTurn(t *testing.T) {
	fakeConvSTT(t, "say it")
	d := &convSpyDispatcher{reply: "boom"}
	synth := &convSpySynth{err: errors.New("tts down")}
	svc := NewConversationService("", d, synth)
	svc.Start("sess-3")
	res, err := svc.Turn(context.Background(), ConversationTurn{Audio: []byte("RIFFb\x00\x00\x00WAVEfmt "), SpeakReply: true})
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if res.Reply != "boom" || res.Audio != nil {
		t.Errorf("result = %+v, want reply text and nil audio", res)
	}
}

func TestConversationTurnDispatchErrorPropagates(t *testing.T) {
	fakeConvSTT(t, "any")
	d := &convSpyDispatcher{err: errors.New("chat down")}
	svc := NewConversationService("", d, nil)
	svc.Start("sess-4")
	_, err := svc.Turn(context.Background(), ConversationTurn{Audio: []byte("RIFFb\x00\x00\x00WAVEfmt ")})
	if err == nil || !errors.Is(err, d.err) {
		t.Fatalf("Turn err = %v, want chat down", err)
	}
}
