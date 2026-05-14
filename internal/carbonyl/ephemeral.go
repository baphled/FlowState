package carbonyl

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EphemeralServer is a short-lived HTTP server that serves both the Vue
// SPA static files and the Go API on a random loopback port. It is the
// bridge between the CLI process and the Carbonyl-rendered SPA.
type EphemeralServer struct {
	server   *http.Server
	listener net.Listener
	baseURL  string
	staticFS fs.FS
}

// NewEphemeralServer creates an EphemeralServer bound to 127.0.0.1:0
// (OS-assigned port). The server is not started; call Start to begin
// serving.
//
// Expected:
//   - handler is a non-nil http.Handler for API routes (/api/*).
//   - staticDir may be empty (API-only mode) or a path to the Vue SPA
//     dist directory containing index.html.
//
// Returns:
//   - A ready-to-start EphemeralServer, or an error if the listener
//     cannot bind or the static directory is invalid.
//
// Side effects:
//   - Binds a TCP listener on 127.0.0.1:0.
func NewEphemeralServer(handler http.Handler, staticDir string) (*EphemeralServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("carbonyl: failed to bind ephemeral listener: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	var staticFS fs.FS
	if staticDir != "" {
		absDir, absErr := filepath.Abs(staticDir)
		if absErr != nil {
			listener.Close()
			return nil, fmt.Errorf("carbonyl: failed to resolve static dir %q: %w", staticDir, absErr)
		}
		if statErr := validateStaticDir(absDir); statErr != nil {
			listener.Close()
			return nil, statErr
		}
		staticFS = os.DirFS(absDir)
	}

	mux := http.NewServeMux()

	if staticFS != nil {
		fileServer := http.FileServer(http.FS(staticFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if isAPIRequest(r.URL.Path) {
				handler.ServeHTTP(w, r)
				return
			}
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticFS, path); err != nil {
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		})
	} else {
		mux.Handle("/", handler)
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return context.Background()
		},
	}

	return &EphemeralServer{
		server:   srv,
		listener: listener,
		baseURL:  baseURL,
		staticFS: staticFS,
	}, nil
}

// Start begins serving HTTP requests in a background goroutine.
func (es *EphemeralServer) Start() error {
	go es.server.Serve(es.listener)
	return nil
}

// URL returns the base URL (e.g. http://127.0.0.1:42137) of the
// ephemeral server.
func (es *EphemeralServer) URL() string {
	return es.baseURL
}

// Port returns the OS-assigned TCP port number.
func (es *EphemeralServer) Port() int {
	return es.listener.Addr().(*net.TCPAddr).Port
}

// Shutdown gracefully stops the server, waiting up to the context
// deadline for in-flight requests to complete.
func (es *EphemeralServer) Shutdown(ctx context.Context) error {
	return es.server.Shutdown(ctx)
}

// isAPIRequest returns true for paths under /api/ which should be
// routed to the Go API handler rather than the SPA file server.
func isAPIRequest(path string) bool {
	return strings.HasPrefix(path, "/api/")
}

// validateStaticDir checks that the given directory exists and
// contains an index.html file.
func validateStaticDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("carbonyl: web frontend not built — static dir %q does not exist. Run `make web-build` or use --no-carbonyl", dir)
		}
		return fmt.Errorf("carbonyl: cannot access static dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("carbonyl: %q is not a directory", dir)
	}
	indexPath := filepath.Join(dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		return fmt.Errorf("carbonyl: web frontend not built — %q not found. Run `make web-build` or use --no-carbonyl", indexPath)
	}
	return nil
}
