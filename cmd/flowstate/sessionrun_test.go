package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// fakeZaiServer serves an SSE stream of the given chunks.
func fakeZaiServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withFakeZai swaps the CLI provider resolver for an httptest-backed
// zai adapter and restores it after the test.
func withFakeZai(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := providerResolver
	providerResolver = func(name string) (provider.Provider, error) {
		return zai.New("test-key", zai.WithBaseURL(srv.URL)), nil
	}
	t.Cleanup(func() { providerResolver = old })
}

func TestGivenNewSessionWhenRunThenStreamsEventsAndCompletes(t *testing.T) {
	srv := fakeZaiServer(t,
		`{"choices":[{"delta":{"content":"he"}}]}`,
		`{"choices":[{"delta":{"content":"y"}}]}`,
	)
	withFakeZai(t, srv)

	created := runCLINew(t, "--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
	if created.exit != 0 {
		t.Fatalf("new exit = %d, stdout %s", created.exit, created.stdout)
	}
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(created.stdout, "\n", 2)[0]), &rec); err != nil {
		t.Fatalf("decode new output: %v (%s)", err, created.stdout)
	}

	run := runCLIRun(t, rec.ID)
	if run.exit != 0 {
		t.Fatalf("run exit = %d, stdout %s", run.exit, run.stdout)
	}
	lines := nonEmpty(strings.Split(run.stdout, "\n"))
	if len(lines) < 4 {
		t.Fatalf("run stdout = %q, want at least 4 event lines", run.stdout)
	}
	var last struct {
		Type    string          `json:"type"`
		Actor   string          `json:"actor"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("decode final line: %v (%s)", err, lines[len(lines)-1])
	}
	if last.Type != "turn.end" {
		t.Fatalf("final line type = %s, want turn.end", last.Type)
	}
	if last.Actor != "engine" {
		t.Fatalf("final line actor = %s, want engine", last.Actor)
	}
	if !strings.Contains(string(last.Payload), `"status":"completed"`) {
		t.Fatalf("final payload = %s, want completed status", last.Payload)
	}

	evts := runCLIEvents(t, rec.ID)
	if evts.exit != 0 {
		t.Fatalf("events exit = %d", evts.exit)
	}
	wantOrder := []string{"turn.start", "user.message", "assistant.delta", "assistant.delta", "assistant.message", "turn.end"}
	gotTypes := []string{}
	for _, l := range nonEmpty(strings.Split(evts.stdout, "\n")) {
		var e struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("decode event line: %v (%s)", err, l)
		}
		gotTypes = append(gotTypes, e.Type)
	}
	if strings.Join(gotTypes, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("event types = %v, want %v", gotTypes, wantOrder)
	}
}

func TestGivenProviderFailureWhenRunThenExitThreeAndProviderErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"502","message":"down"}}`))
	}))
	t.Cleanup(srv.Close)
	withFakeZai(t, srv)

	created := runCLINew(t, "--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
	if created.exit != 0 {
		t.Fatalf("new exit = %d", created.exit)
	}
	var rec struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.SplitN(created.stdout, "\n", 2)[0]), &rec)

	run := runCLIRun(t, rec.ID)
	if run.exit != 3 {
		t.Fatalf("run exit = %d, want 3 (provider error)", run.exit)
	}
	lines := nonEmpty(strings.Split(run.stdout, "\n"))
	var last struct {
		Type string `json:"type"`
	}
	for _, l := range lines {
		var e struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &e); err != nil || e.Type == "" {
			continue
		}
		last.Type = e.Type
	}
	if last.Type != "provider.error" {
		t.Fatalf("final line type = %s, want provider.error", last.Type)
	}
}

func TestGivenMissingSessionIDWhenRunThenUsage(t *testing.T) {
	if got := runCLIRun(t); got.exit != 2 {
		t.Fatalf("run exit = %d, want 2", got.exit)
	}
}

func TestGivenUnknownSessionWhenRunThenExitOne(t *testing.T) {
	if got := runCLIRun(t, "nope"); got.exit != 1 {
		t.Fatalf("run exit = %d, want 1", got.exit)
	}
}

func TestGivenTerminalSessionWhenRunThenExitSix(t *testing.T) {
	srv := fakeZaiServer(t, `{"choices":[{"delta":{"content":"x"}}]}`)
	withFakeZai(t, srv)
	created := runCLINew(t, "--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
	var rec struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.SplitN(created.stdout, "\n", 2)[0]), &rec)
	first := runCLIRun(t, rec.ID)
	if first.exit != 0 {
		t.Fatalf("first run exit = %d (%s)", first.exit, first.stdout)
	}
	if got := runCLIRun(t, rec.ID); got.exit != 6 {
		t.Fatalf("second run exit = %d, want 6", got.exit)
	}
}

// runCLIRun runs `session run` with the given id args against the
// per-test temp DB.
func runCLIRun(t *testing.T, ids ...string) result {
	t.Helper()
	return runArgv(t, append([]string{"session", "run", "--db", dbFor(t)}, ids...))
}

// nonEmpty drops empty split fragments.
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
