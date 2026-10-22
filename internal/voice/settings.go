package voice

// Settings is the runtime voice settings snapshot exposed via
// GET/PATCH /api/v1/voice/settings. It mirrors the tunable subset
// of config.VoiceConfig; changes are in-memory runtime state and do
// not persist to YAML.
type Settings struct {
	// TTSEnabled opts in to spoken responses.
	TTSEnabled bool `json:"enabled"`
	// TTSModel is the piper voice model name.
	TTSModel string `json:"tts_model"`
	// LengthScale is piper's speech-rate control.
	LengthScale float64 `json:"length_scale"`
	// NoiseScale is piper's prosody control.
	NoiseScale float64 `json:"noise_scale"`
	// SentenceSilence is the inter-sentence gap in seconds.
	SentenceSilence float64 `json:"sentence_silence"`
}

// SettingsPatch is the partial-update payload for PATCH
// /api/v1/voice/settings; nil fields are left unchanged.
type SettingsPatch struct {
	// TTSEnabled toggles spoken output when present.
	TTSEnabled *bool `json:"enabled,omitempty"`
	// TTSModel switches the piper voice when present.
	TTSModel *string `json:"tts_model,omitempty"`
	// LengthScale adjusts speech rate when present.
	LengthScale *float64 `json:"length_scale,omitempty"`
	// NoiseScale adjusts prosody when present.
	NoiseScale *float64 `json:"noise_scale,omitempty"`
	// SentenceSilence adjusts the inter-sentence gap when present.
	SentenceSilence *float64 `json:"sentence_silence,omitempty"`
}
