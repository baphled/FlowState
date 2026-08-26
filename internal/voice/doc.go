// Package voice implements the local-only voice pipeline for the
// "talk to agents" surface: audio capture (parecord/arecord), speech-
// to-text (whisper-cli), and text-to-speech (piper/espeak-ng). Every
// stage shells out to a POSIX command whose invocation is
// env-overridable (FLOWSTATE_VOICE_CAPTURE / _STT / _TTS) so tests can
// substitute fake binaries without audio hardware. The package is an
// input adapter only: transcripts are handed to the existing
// dispatch.Dispatcher via the App layer — no engine changes.
package voice
