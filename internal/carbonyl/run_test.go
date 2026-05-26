package carbonyl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockApp struct {
	setAgentCalled bool
	setAgentID     string
	eventBus       EventBus
	sessionMgr     SessionRegistrar
	sessionsDir    string
	apiServer      APIServer
}

func (m *mockApp) SetAgentManifest(agentID string) {
	m.setAgentCalled = true
	m.setAgentID = agentID
}

func (m *mockApp) EventBus() EventBus           { return m.eventBus }
func (m *mockApp) SessionMgr() SessionRegistrar { return m.sessionMgr }
func (m *mockApp) SessionsDir() string          { return m.sessionsDir }
func (m *mockApp) APIServer() APIServer         { return m.apiServer }

type mockSessionRegistrar struct {
	registerCalled bool
	sessionID      string
	agentID        string
}

func (m *mockSessionRegistrar) RegisterSession(sessionID string, agentID string) {
	m.registerCalled = true
	m.sessionID = sessionID
	m.agentID = agentID
}

type mockAPIServer struct {
	handler http.Handler
}

func (m *mockAPIServer) Handler() http.Handler { return m.handler }


func TestBuildTargetURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		agentID   string
		sessionID string
		want      string
	}{
		{
			name:    "both empty still emits carbonyl flag",
			baseURL: "http://127.0.0.1:42137",
			want:    "http://127.0.0.1:42137?carbonyl=1",
		},
		{
			name:      "session only",
			baseURL:   "http://127.0.0.1:42137",
			sessionID: "sess-123",
			want:      "http://127.0.0.1:42137?carbonyl=1&session=sess-123",
		},
		{
			name:    "agent only",
			baseURL: "http://127.0.0.1:42137",
			agentID: "my-agent",
			want:    "http://127.0.0.1:42137?carbonyl=1&agent=my-agent",
		},
		{
			name:      "both present",
			baseURL:   "http://127.0.0.1:42137",
			agentID:   "my-agent",
			sessionID: "sess-123",
			want:      "http://127.0.0.1:42137?carbonyl=1&session=sess-123&agent=my-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTargetURL(tt.baseURL, tt.agentID, tt.sessionID)
			if got != tt.want {
				t.Errorf("buildTargetURL(%q, %q, %q) = %q, want %q",
					tt.baseURL, tt.agentID, tt.sessionID, got, tt.want)
			}
			// Defence-in-depth: even if the table-driven `want` is ever
			// updated incorrectly, the carbonyl flag must always be
			// present — the Vue boot path keys focus stealing off it.
			if !strings.Contains(got, "carbonyl=1") {
				t.Errorf("buildTargetURL(%q, %q, %q) = %q, missing carbonyl=1 flag",
					tt.baseURL, tt.agentID, tt.sessionID, got)
			}
		})
	}
}

func TestRunSetsAgentManifest(t *testing.T) {
	mgr := &mockSessionRegistrar{}
	app := &mockApp{
		apiServer:  &mockAPIServer{handler: http.NewServeMux()},
		sessionMgr: mgr,
	}

	fakeBin := makeFakeBinary(t, "fake-carbonyl", "sleep 300")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{}, 1)
	go func() {
		opts := RunOptions{
			BinaryPath: fakeBin,
			FPS:        15,
			Zoom:       100,
			StaticDir:  "",
			APIMux:     http.NewServeMux(),
			Stdin:      nil,
			Stdout:     io.Discard,
			Stderr:     io.Discard,
		}
		_ = RunWithOptions(app, "test-agent", "sess-1", opts)
		done <- struct{}{}
	}()

	// Wait briefly for RunWithOptions to execute the pre-flight steps.
	time.Sleep(3 * time.Second)

	if !app.setAgentCalled {
		t.Error("SetAgentManifest should have been called with non-empty agentID")
	}
	if app.setAgentID != "test-agent" {
		t.Errorf("SetAgentManifest agentID = %q, want %q", app.setAgentID, "test-agent")
	}
	if !mgr.registerCalled {
		t.Error("RegisterSession should have been called")
	}

	cancel()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func TestRunEmptyAgentSkipsManifest(t *testing.T) {
	app := &mockApp{
		apiServer: &mockAPIServer{handler: http.NewServeMux()},
	}

	fakeBin := makeFakeBinary(t, "fake-carbonyl", "sleep 300")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{}, 1)
	go func() {
		opts := RunOptions{
			BinaryPath: fakeBin,
			FPS:        15,
			Zoom:       100,
			StaticDir:  "",
			APIMux:     http.NewServeMux(),
			Stdin:      nil,
			Stdout:     io.Discard,
			Stderr:     io.Discard,
		}
		_ = RunWithOptions(app, "", "sess-2", opts)
		done <- struct{}{}
	}()

	time.Sleep(3 * time.Second)

	if app.setAgentCalled {
		t.Error("SetAgentManifest should NOT have been called when agentID is empty")
	}

	cancel()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func TestPersistSessionMetadata(t *testing.T) {
	dir := t.TempDir()
	persistSessionMetadata(dir, "sess-meta", "agent-1")

	metaPath := filepath.Join(dir, "sess-meta.meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading metadata file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `"sess-meta"`) {
		t.Errorf("metadata file should contain session ID, got: %s", content)
	}
	if !strings.Contains(content, `"agent-1"`) {
		t.Errorf("metadata file should contain agent ID, got: %s", content)
	}
}

func TestPersistSessionMetadataEmptyDir(t *testing.T) {
	// Should not panic or create files when sessionsDir is empty.
	persistSessionMetadata("", "sess-1", "agent-1")
}

func TestPersistSessionMetadataEmptySessionID(t *testing.T) {
	dir := t.TempDir()
	persistSessionMetadata(dir, "", "agent-1")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files when sessionID is empty, got %d", len(entries))
	}
}

func TestResolveCarbonylBinaryEnvOverride(t *testing.T) {
	t.Setenv("CARBONYL_BINARY", "/custom/carbonyl")
	got := resolveCarbonylBinary()
	if got != "/custom/carbonyl" {
		t.Errorf("resolveCarbonylBinary() = %q, want %q", got, "/custom/carbonyl")
	}
}

func TestResolveStaticDirEnvOverride(t *testing.T) {
	dir := t.TempDir()
indexPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOWSTATE_WEB_DIR", dir)
	got := resolveStaticDir()
	if got != dir {
		t.Errorf("resolveStaticDir() = %q, want %q", got, dir)
	}
}

func TestDefaultRunOptions(t *testing.T) {
	opts := DefaultRunOptions()
	if opts.FPS != defaultFPS {
		t.Errorf("DefaultRunOptions().FPS = %d, want %d", opts.FPS, defaultFPS)
	}
	if opts.Zoom != defaultZoom {
		t.Errorf("DefaultRunOptions().Zoom = %d, want %d", opts.Zoom, defaultZoom)
	}
}

func TestRunNoAPIServer(t *testing.T) {
	app := &mockApp{apiServer: nil}
	err := RunWithOptions(app, "agent", "sess", RunOptions{})
	if err == nil {
		t.Fatal("expected error when no API server is available")
	}
	if !strings.Contains(err.Error(), "no API handler") {
		t.Errorf("error = %q, want mention of 'no API handler'", err.Error())
	}
}

func TestPrintURLNoAPIServer(t *testing.T) {
	app := &mockApp{apiServer: nil}
	err := PrintURL(app, "agent", "sess")
	if err == nil {
		t.Fatal("expected error when no API server is available")
	}
}

func TestOpenInBrowserNoAPIServer(t *testing.T) {
	app := &mockApp{apiServer: nil}
	err := OpenInBrowser(app, "agent", "sess")
	if err == nil {
		t.Fatal("expected error when no API server is available")
	}
}

func TestPublishResumedEventNoPanic(t *testing.T) {
	// Should not panic with nil EventBus.
	publishResumedEvent(nil, "sess-1")
	publishResumedEvent(nil, "")
}

func TestAppInterfaceContract(t *testing.T) {
	// Verify that mockApp satisfies AppInterface at compile time.
	var _ AppInterface = &mockApp{}
}

func TestHTTPTestServerAsAPIMux(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	apiHandler := srv.Config.Handler
	ephemeral, err := NewEphemeralServer(apiHandler, "")
	if err != nil {
		t.Fatalf("NewEphemeralServer: %v", err)
	}
	if err := ephemeral.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ephemeral.Shutdown(ctx)
	}()

	resp, err := http.Get(ephemeral.URL() + "/api/v1/sessions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestRunCreatesSessionMetadata(t *testing.T) {
	sessionsDir := t.TempDir()
	mgr := &mockSessionRegistrar{}
	app := &mockApp{
		apiServer:   &mockAPIServer{handler: http.NewServeMux()},
		sessionMgr:  mgr,
		sessionsDir: sessionsDir,
	}

	fakeBin := makeFakeBinary(t, "fake-carbonyl", "sleep 300")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{}, 1)
	go func() {
		opts := RunOptions{
			BinaryPath: fakeBin,
			FPS:        15,
			Zoom:       100,
			StaticDir:  "",
			APIMux:     http.NewServeMux(),
			Stdin:      nil,
			Stdout:     io.Discard,
			Stderr:     io.Discard,
		}
		_ = RunWithOptions(app, "my-agent", "sess-abc", opts)
		done <- struct{}{}
	}()

	time.Sleep(5 * time.Second)

	metaPath := filepath.Join(sessionsDir, "sess-abc.meta.json")
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Error("session metadata file should have been created")
	}

	cancel()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func TestErrNoAPIHandlerReturned(t *testing.T) {
	app := &mockApp{}
	err := Run(app, "agent", "sess")
	if err == nil {
		t.Fatal("expected error when no API handler is available")
	}
	if err != errNoAPIHandler {
		t.Errorf("error = %v, want errNoAPIHandler", err)
	}
}
