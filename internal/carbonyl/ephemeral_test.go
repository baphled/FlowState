package carbonyl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEphemeralServerStartsAndServes(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v1/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	es, err := NewEphemeralServer(handler, "")
	if err != nil {
		t.Fatalf("NewEphemeralServer: %v", err)
	}
	if err := es.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		es.Shutdown(ctx)
	}()

	if es.URL() == "" {
		t.Error("URL() returned empty string")
	}
	if es.Port() == 0 {
		t.Error("Port() returned 0")
	}

	resp, err := http.Get(es.URL() + "/api/v1/ping")
	if err != nil {
		t.Fatalf("GET /api/v1/ping: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestEphemeralServerWithStaticFiles(t *testing.T) {
	staticDir := t.TempDir()
	indexContent := `<!DOCTYPE html><html><body>FlowState</body></html>`
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte(indexContent), 0644); err != nil {
		t.Fatal(err)
	}

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v1/test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	es, err := NewEphemeralServer(handler, staticDir)
	if err != nil {
		t.Fatalf("NewEphemeralServer: %v", err)
	}
	if err := es.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		es.Shutdown(ctx)
	}()

	// Static file serving: root URL should serve index.html.
	resp, err := http.Get(es.URL() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("root status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// API routes should still work.
	resp2, err := http.Get(es.URL() + "/api/v1/test")
	if err != nil {
		t.Fatalf("GET /api/v1/test: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("api status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}
}

func TestEphemeralServerMissingIndexHTML(t *testing.T) {
	emptyDir := t.TempDir()
	handler := http.NewServeMux()

	_, err := NewEphemeralServer(handler, emptyDir)
	if err == nil {
		t.Fatal("expected error when static dir has no index.html")
	}
}

func TestEphemeralServerNonexistentDir(t *testing.T) {
	handler := http.NewServeMux()
	_, err := NewEphemeralServer(handler, "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent static dir")
	}
}

func TestEphemeralServerAPIOnly(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	es, err := NewEphemeralServer(handler, "")
	if err != nil {
		t.Fatalf("NewEphemeralServer: %v", err)
	}
	if es.staticFS != nil {
		t.Error("staticFS should be nil when staticDir is empty")
	}
	if err := es.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		es.Shutdown(ctx)
	}()

	resp, err := http.Get(es.URL() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestIsAPIRequest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/sessions", true},
		{"/api/v1/agents/123", true},
		{"/api/", true},
		{"/", false},
		{"/index.html", false},
		{"/assets/main.js", false},
	}

	for _, tt := range tests {
		got := isAPIRequest(tt.path)
		if got != tt.want {
			t.Errorf("isAPIRequest(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestEphemeralServerServesAPIWithHTTPTestServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"sessions":[]}`))
	}))
	defer ts.Close()

	es, err := NewEphemeralServer(ts.Config.Handler, "")
	if err != nil {
		t.Fatalf("NewEphemeralServer: %v", err)
	}
	if err := es.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		es.Shutdown(ctx)
	}()

	resp, err := http.Get(es.URL() + "/api/v1/sessions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
