package ollama

import "testing"

// TestResolveOllamaContextLength covers the lookup table installed to
// close the failover.Manager.ResolveContextLength gap where every
// Ollama model previously fell through to the generic 4096 fallback.
// The failover manager asks the provider for models and reads
// ContextLength per model, so correct resolution here is the only way
// the auto-compactor can know when a 70% threshold of a 131k-token
// window has been crossed.
func TestResolveOllamaContextLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want int
	}{
		{"llama 3.2 latest tag resolves to 131k", "llama3.2:latest", 131072},
		{"llama 3.2 size tag resolves to 131k", "llama3.2:3b", 131072},
		{"llama 3.1 resolves to 131k", "llama3.1:8b", 131072},
		{"llama 3 without dot resolves to 8k", "llama3:latest", 8192},
		{"qwen 2.5 instruct resolves to 32k", "qwen2.5:7b-instruct", 32768},
		{"qwen 2 resolves to 32k", "qwen2:7b", 32768},
		{"granite 4 resolves to 131k", "granite4:tiny", 131072},
		{"granite 3 resolves to 131k", "granite3:8b", 131072},
		{"mistral tag resolves to 32k", "mistral:latest", 32768},
		{"mixtral resolves to 32k", "mixtral:8x7b", 32768},
		{"phi 3 resolves to 131k", "phi3:mini", 131072},
		{"gemma 2 resolves to 8k", "gemma2:9b", 8192},
		{"codellama resolves to 16k", "codellama:7b", 16384},
		{"deepseek resolves to 16k", "deepseek:7b", 16384},
		{"case-insensitive prefix match", "LLaMA3.2:latest", 131072},
		{"empty string returns default", "", 4096},
		{"unknown family returns default", "some-custom-model:v1", 4096},
		{"longest-prefix wins over short prefix", "llama3.2:70b", 131072},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveOllamaContextLength(tc.in); got != tc.want {
				t.Errorf("resolveOllamaContextLength(%q) = %d; want %d", tc.in, got, tc.want)
			}
		})
	}
}
