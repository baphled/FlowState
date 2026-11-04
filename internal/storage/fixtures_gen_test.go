package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateFixtures writes frozen canonical-JSON byte fixtures used by the
// replay-oracle qualification. Regenerate deliberately; fixtures are frozen.
func TestGenerateFixtures(t *testing.T) {
	if os.Getenv("GEN_FIXTURES") == "" {
		t.Skip("set GEN_FIXTURES=1 to regenerate fixtures")
	}
	dir := "testdata"
	cases := []struct {
		name string
		v    map[string]any
	}{
		{"key_order", map[string]any{"zeta": 1, "alpha": 2, "Mid": 3, "_under": 4}},
		{"unicode", map[string]any{"text": "héllo → 世界", "emoji": "🚀"}},
		{"numbers", map[string]any{"int": 3, "float": 1.5, "whole": 2.0, "big": 9007199254740993.0}},
		{"nested", map[string]any{"b": 1, "a": []any{1.25, 2, "x"}, "o": map[string]any{"y": true, "x": nil}}},
	}
	for _, c := range cases {
		canon, err := CanonicalJSON(c.v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.name+".json"), []byte(canon), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
