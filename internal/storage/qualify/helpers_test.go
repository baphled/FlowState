package qualify

import (
	"encoding/json"
	"sync"
	"testing"
)

func jsonNum(s string) json.Number { return json.Number(s) }

var (
	reopenMu   sync.Mutex
	reopenPath string
)

// reopenPathOf registers and returns the DB file path that later steps of the
// same test should reopen. Only one path per test (the first caller wins).
func reopenPathOf(t *testing.T, name string) string {
	t.Helper()
	reopenMu.Lock()
	defer reopenMu.Unlock()
	if reopenPath == "" {
		reopenPath = t.TempDir() + "/" + name
		t.Cleanup(func() { reopenMu.Lock(); reopenPath = ""; reopenMu.Unlock() })
	}
	return reopenPath
}
