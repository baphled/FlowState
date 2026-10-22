package voice

import (
	"context"
	"fmt"
	"os"
)

// transcribeAudio stages WAV bytes in a temporary file and runs the
// STT tool over it.
//
// Expected:
//   - ctx is non-nil; cancellation aborts the STT child.
//   - audio is non-empty WAV bytes.
//
// Returns:
//   - The transcript.
//   - ErrSTTUnavailable when no STT binary resolves.
//
// Side effects:
//   - Writes and removes a temporary WAV; spawns the STT child.
func transcribeAudio(ctx context.Context, stt string, audio []byte) (string, error) {
	tmp, err := os.CreateTemp("", "flowstate-voice-conversation-*.wav")
	if err != nil {
		return "", fmt.Errorf("voice: stage audio: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(audio); err != nil {
		tmp.Close()
		return "", fmt.Errorf("voice: stage audio: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("voice: stage audio: %w", err)
	}
	sttTool, err := NewSTTTool(stt)
	if err != nil {
		return "", err
	}
	return sttTool.Transcribe(ctx, path)
}
