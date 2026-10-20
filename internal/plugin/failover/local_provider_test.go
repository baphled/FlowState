package failover

import "testing"

// TestIsLocalProvider covers the local-provider classifier used by the
// strict fallback tail: ollama (any ASCII casing) is local; hosted
// adapters and unknown/empty names are not.
func TestIsLocalProvider(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		want     bool
	}{
		{"ollama", "ollama", true},
		{"ollama uppercase", "Ollama", true},
		{"ollama mixed case", "OLLAMA", true},
		{"anthropic", "anthropic", false},
		{"openai", "openai", false},
		{"zai", "zai", false},
		{"ollamacloud is hosted", "ollamacloud", false},
		{"empty", "", false},
		{"unknown", "some-new-adapter", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLocalProvider(tc.provider); got != tc.want {
				t.Fatalf("IsLocalProvider(%q) = %v, want %v", tc.provider, got, tc.want)
			}
		})
	}
}

// TestLowerASCII verifies the allocation-free ASCII lowercasing helper
// round-trips the common provider-name shapes.
func TestLowerASCII(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ollama", "ollama"},
		{"Ollama", "ollama"},
		{"OLLAMA", "ollama"},
		{"zAi", "zai"},
		{"", ""},
		{"aBcDeF", "abcdef"},
	}
	for _, tc := range cases {
		if got := lowerASCII(tc.in); got != tc.want {
			t.Fatalf("lowerASCII(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
