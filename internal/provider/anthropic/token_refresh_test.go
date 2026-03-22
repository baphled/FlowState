package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTPTokenRefresher", func() {
	var (
		server    *httptest.Server
		refresher *HTTPTokenRefresher
		ctx       context.Context
	)

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Context("when the token endpoint returns valid JSON", func() {
		BeforeEach(func() {
			ctx = context.Background()
			server = httptest.NewServer(
				http.HandlerFunc(refreshHandler(http.StatusOK,
					`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)),
			)
			refresher = &HTTPTokenRefresher{
				Client:        server.Client(),
				TokenEndpoint: server.URL,
			}
		})

		It("returns new tokens and an expiry", func() {
			result, err := refresher.Refresh(ctx, "old-refresh")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.AccessToken).To(Equal("new-access"))
			Expect(result.RefreshToken).To(Equal("new-refresh"))
			Expect(result.ExpiresAt).To(
				BeNumerically(">", time.Now().UnixMilli()),
			)
		})
	})

	Context("when the endpoint returns a non-200 status", func() {
		BeforeEach(func() {
			ctx = context.Background()
			server = httptest.NewServer(
				http.HandlerFunc(refreshHandler(
					http.StatusUnauthorized, `{"error":"invalid"}`,
				)),
			)
			refresher = &HTTPTokenRefresher{
				Client:        server.Client(),
				TokenEndpoint: server.URL,
			}
		})

		It("returns an error with the status code", func() {
			_, err := refresher.Refresh(ctx, "bad-refresh")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("status 401"))
		})
	})

	Context("when the endpoint returns invalid JSON", func() {
		BeforeEach(func() {
			ctx = context.Background()
			server = httptest.NewServer(
				http.HandlerFunc(refreshHandler(
					http.StatusOK, `{broken`,
				)),
			)
			refresher = &HTTPTokenRefresher{
				Client:        server.Client(),
				TokenEndpoint: server.URL,
			}
		})

		It("returns a decode error", func() {
			_, err := refresher.Refresh(ctx, "refresh-tok")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(
				ContainSubstring("decoding refresh response"),
			)
		})
	})

	Context("when the response has an empty access token", func() {
		BeforeEach(func() {
			ctx = context.Background()
			server = httptest.NewServer(
				http.HandlerFunc(refreshHandler(http.StatusOK,
					`{"access_token":"","refresh_token":"r","expires_in":3600}`)),
			)
			refresher = &HTTPTokenRefresher{
				Client:        server.Client(),
				TokenEndpoint: server.URL,
			}
		})

		It("returns errEmptyAccessToken", func() {
			_, err := refresher.Refresh(ctx, "refresh-tok")
			Expect(err).To(MatchError(errEmptyAccessToken))
		})
	})

	Context("when the endpoint is unreachable", func() {
		BeforeEach(func() {
			ctx = context.Background()
			refresher = &HTTPTokenRefresher{
				Client:        http.DefaultClient,
				TokenEndpoint: "http://127.0.0.1:1",
			}
		})

		It("returns a connection error", func() {
			_, err := refresher.Refresh(ctx, "refresh-tok")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(
				ContainSubstring("executing token refresh"),
			)
		})
	})

	Context("when using the default endpoint", func() {
		It("sets TokenEndpoint to the Anthropic default", func() {
			r := &HTTPTokenRefresher{Client: http.DefaultClient}
			Expect(r.TokenEndpoint).To(BeEmpty())
		})
	})
})

var _ = Describe("TokenManager", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("NewDirectTokenManager", func() {
		It("returns cached token without refreshing", func() {
			tm := NewDirectTokenManager("static-token")
			token, err := tm.EnsureToken(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("static-token"))
		})

		It("has a far-future expiry", func() {
			tm := NewDirectTokenManager("static-token")
			Expect(tm.ExpiresAt()).To(BeNumerically(
				">", time.Now().UnixMilli()+86400000,
			))
		})
	})

	Describe("EnsureToken", func() {
		Context("when token is not expired", func() {
			It("returns the cached token without calling refresher", func() {
				spy := &spyRefresher{}
				tm := NewTokenManager(
					"valid-token", "refresh-tok",
					time.Now().UnixMilli()+3600000,
					spy, "",
				)
				token, err := tm.EnsureToken(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("valid-token"))
				Expect(spy.callCount).To(Equal(0))
			})
		})

		Context("when token is expired", func() {
			It("refreshes and returns the new token", func() {
				spy := &spyRefresher{
					result: RefreshResult{
						AccessToken:  "refreshed-access",
						RefreshToken: "refreshed-refresh",
						ExpiresAt:    time.Now().UnixMilli() + 7200000,
					},
				}
				tm := NewTokenManager(
					"old-token", "refresh-tok",
					time.Now().UnixMilli()-1000,
					spy, "",
				)
				token, err := tm.EnsureToken(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("refreshed-access"))
				Expect(spy.callCount).To(Equal(1))
			})
		})

		Context("when token is within the 5-minute buffer", func() {
			It("refreshes proactively", func() {
				spy := &spyRefresher{
					result: RefreshResult{
						AccessToken:  "proactive-access",
						RefreshToken: "proactive-refresh",
						ExpiresAt:    time.Now().UnixMilli() + 7200000,
					},
				}
				bufferEdge := time.Now().UnixMilli() + refreshBufferMs - 1000
				tm := NewTokenManager(
					"soon-expiring", "refresh-tok",
					bufferEdge, spy, "",
				)
				token, err := tm.EnsureToken(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("proactive-access"))
				Expect(spy.callCount).To(Equal(1))
			})
		})

		Context("when refresher is nil", func() {
			It("returns the cached token even if expired", func() {
				tm := NewTokenManager(
					"stale-token", "", 0, nil, "",
				)
				token, err := tm.EnsureToken(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("stale-token"))
			})
		})

		Context("when refresh fails", func() {
			It("returns the error", func() {
				spy := &spyRefresher{
					err: fmt.Errorf("network down"),
				}
				tm := NewTokenManager(
					"old-token", "refresh-tok",
					time.Now().UnixMilli()-1000,
					spy, "",
				)
				_, err := tm.EnsureToken(ctx)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(
					ContainSubstring("network down"),
				)
			})
		})

		Context("when access token is empty", func() {
			It("triggers a refresh", func() {
				spy := &spyRefresher{
					result: RefreshResult{
						AccessToken:  "new-access",
						RefreshToken: "new-refresh",
						ExpiresAt:    time.Now().UnixMilli() + 7200000,
					},
				}
				tm := NewTokenManager(
					"", "refresh-tok",
					time.Now().UnixMilli()+3600000,
					spy, "",
				)
				token, err := tm.EnsureToken(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(token).To(Equal("new-access"))
			})
		})

		Context("thread safety", func() {
			It("handles concurrent calls without panic", func() {
				spy := &spyRefresher{
					result: RefreshResult{
						AccessToken:  "concurrent-access",
						RefreshToken: "concurrent-refresh",
						ExpiresAt:    time.Now().UnixMilli() + 7200000,
					},
				}
				tm := NewTokenManager(
					"old", "refresh-tok",
					time.Now().UnixMilli()-1000,
					spy, "",
				)
				var wg sync.WaitGroup
				for range 10 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := tm.EnsureToken(ctx)
						Expect(err).NotTo(HaveOccurred())
					}()
				}
				wg.Wait()
				Expect(tm.AccessToken()).To(
					Equal("concurrent-access"),
				)
			})
		})
	})

	Describe("persistTokens", func() {
		var tmpDir string

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "token-persist-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tmpDir)
		})

		It("updates auth.json with refreshed tokens", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			initial := map[string]interface{}{
				"anthropic": map[string]interface{}{
					"type":    "oauth",
					"access":  "old-access",
					"refresh": "old-refresh",
					"expires": float64(1000),
				},
				"github-copilot": map[string]interface{}{
					"type":   "oauth",
					"access": "gho_keep",
				},
			}
			data, _ := json.MarshalIndent(initial, "", "  ")
			Expect(os.WriteFile(
				authPath, data, authFilePermissions,
			)).To(Succeed())

			spy := &spyRefresher{
				result: RefreshResult{
					AccessToken:  "persisted-access",
					RefreshToken: "persisted-refresh",
					ExpiresAt:    9999999,
				},
			}
			tm := NewTokenManager(
				"old-access", "old-refresh", 0,
				spy, authPath,
			)
			_, err := tm.EnsureToken(ctx)
			Expect(err).NotTo(HaveOccurred())

			saved, err := os.ReadFile(authPath)
			Expect(err).NotTo(HaveOccurred())

			var parsed map[string]json.RawMessage
			Expect(json.Unmarshal(saved, &parsed)).To(Succeed())

			var anthro map[string]interface{}
			Expect(json.Unmarshal(
				parsed["anthropic"], &anthro,
			)).To(Succeed())
			Expect(anthro["access"]).To(
				Equal("persisted-access"),
			)
			Expect(anthro["refresh"]).To(
				Equal("persisted-refresh"),
			)

			var copilot map[string]interface{}
			Expect(json.Unmarshal(
				parsed["github-copilot"], &copilot,
			)).To(Succeed())
			Expect(copilot["access"]).To(Equal("gho_keep"))
		})

		It("preserves file permissions", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			initial := `{"anthropic":{"type":"oauth","access":"x","refresh":"y","expires":1}}`
			Expect(os.WriteFile(
				authPath, []byte(initial), authFilePermissions,
			)).To(Succeed())

			spy := &spyRefresher{
				result: RefreshResult{
					AccessToken:  "new",
					RefreshToken: "new-r",
					ExpiresAt:    2000,
				},
			}
			tm := NewTokenManager(
				"x", "y", 0, spy, authPath,
			)
			_, err := tm.EnsureToken(ctx)
			Expect(err).NotTo(HaveOccurred())

			info, err := os.Stat(authPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(
				Equal(os.FileMode(authFilePermissions)),
			)
		})

		It("skips persistence when authFilePath is empty", func() {
			spy := &spyRefresher{
				result: RefreshResult{
					AccessToken:  "no-persist",
					RefreshToken: "no-persist-r",
					ExpiresAt:    9999999,
				},
			}
			tm := NewTokenManager(
				"old", "refresh-tok", 0, spy, "",
			)
			token, err := tm.EnsureToken(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("no-persist"))
		})
	})

	Describe("AccessToken", func() {
		It("returns the current access token", func() {
			tm := NewDirectTokenManager("my-token")
			Expect(tm.AccessToken()).To(Equal("my-token"))
		})
	})

	Describe("SetExpiresAt", func() {
		It("overrides the expiry time", func() {
			tm := NewDirectTokenManager("tok")
			tm.SetExpiresAt(42)
			Expect(tm.ExpiresAt()).To(
				BeNumerically("==", 42),
			)
		})
	})
})

func refreshHandler(
	status int, body string,
) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

type spyRefresher struct {
	result    RefreshResult
	err       error
	callCount int
	mu        sync.Mutex
}

func (s *spyRefresher) Refresh(
	_ context.Context,
	_ string,
) (RefreshResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount++
	if s.err != nil {
		return RefreshResult{}, s.err
	}
	return s.result, nil
}
