// Package api_test — PR5 dashboard handler specs (Quota Plan PR5
// reviewer REV-1 backfill).
//
// Pins the wire surface for the two PR5 endpoints:
//
//   - GET  /api/v1/providers/quota
//     (api/quota_dashboard.go:83 (handleListProviderQuotas))
//   - POST /api/v1/providers/quota/reset
//     (api/quota_dashboard.go:144 (handleResetProviderQuota))
//
// Coverage matches the reviewer's matrix:
//   - 501 not_implemented when quotaAggregator == nil (feature off)
//   - 405 method_not_allowed on wrong method
//   - 400 invalid_request on malformed body + DisallowUnknownFields
//   - 404 not_found on missing snapshot (reset of unknown key)
//   - 200 happy path (list + reset)
//   - 401 wire-shape parity with peer protected endpoint
//     (GET /api/v1/sessions) — Auth Track PR3 B8 discipline carry-through
//
// Seam-level Ginkgo per memory feedback_ginkgo_not_godog. Drives
// requests via httptest at srv.Handler().ServeHTTP, mirroring the
// auth_whoami_test.go pattern.
package api_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/provider/quota"
)

// fakeQuotaAggregator is a hand-rolled api.QuotaAggregator the
// handler specs drive. The production impl lives at
// internal/app/quota_wireup.go and adapts an *engine.Engine; here
// we want canned snapshots and observable Reset calls so each row
// in the reviewer matrix lands on the exact handler branch under
// test without standing up the engine fixture chain.
type fakeQuotaAggregator struct {
	snapshots []api.QuotaAggregatorRow

	// resetReturnFound / resetReturnErr drive ResetQuotaSpend's
	// (bool, error) return. Defaults: (false, nil) — the not-found
	// path the 404 branch exercises.
	resetReturnFound bool
	resetReturnErr   error

	// resetCalls records every (provider, account, model) the
	// handler delegated to. Asserted by the happy-path spec to pin
	// that the JSON body actually threads through into the aggregator
	// call.
	resetCalls []resetCall
}

type resetCall struct {
	provider    string
	accountHash string
	model       string
}

func (f *fakeQuotaAggregator) QuotaSnapshots(_ context.Context) []api.QuotaAggregatorRow {
	return f.snapshots
}

func (f *fakeQuotaAggregator) ResetQuotaSpend(_ context.Context, providerID, accountHash, modelID string) (bool, error) {
	f.resetCalls = append(f.resetCalls, resetCall{provider: providerID, accountHash: accountHash, model: modelID})
	return f.resetReturnFound, f.resetReturnErr
}

var _ = Describe("PR5 — GET /api/v1/providers/quota + POST /api/v1/providers/quota/reset (REV-1)", func() {
	var registry *agent.Registry

	BeforeEach(func() {
		registry = agent.NewRegistry()
	})

	Context("aggregator unwired (feature off)", func() {
		var srv *api.Server

		BeforeEach(func() {
			// No WithQuotaAggregator(...) — s.quotaAggregator stays nil
			// so the handler's 501 not_implemented branch fires.
			srv = api.NewServer(nil, registry, nil, nil)
		})

		It("returns 501 not_implemented on GET when quotaAggregator == nil (PR4 wiring incomplete)", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/quota", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotImplemented),
				"empty aggregator surfaces as 501 so the SPA can distinguish 'feature unwired' from 'no providers observed yet' (which is 200 + [])")
			Expect(rec.Body.String()).To(ContainSubstring("not_implemented"))
		})

		It("returns 501 not_implemented on POST /reset when quotaAggregator == nil", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
				strings.NewReader(`{"provider":"anthropic","account_hash":"acc","model":"claude-opus-4-7"}`))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotImplemented))
			Expect(rec.Body.String()).To(ContainSubstring("not_implemented"))
		})
	})

	Context("aggregator wired, auth bundle inactive (flag-off pass-through)", func() {
		// WithAuth omitted — registerProtected passes through to plain
		// mux registration so the handler runs without a session cookie.
		// This is the matrix axis where we can directly exercise method,
		// body validation, 404, and happy-path branches without standing
		// up the full auth wire-up.
		var (
			aggregator *fakeQuotaAggregator
			srv        *api.Server
		)

		BeforeEach(func() {
			aggregator = &fakeQuotaAggregator{}
			srv = api.NewServer(nil, registry, nil, nil,
				api.WithQuotaAggregator(aggregator),
			)
		})

		Context("method gate", func() {
			It("returns 405 method_not_allowed on POST /api/v1/providers/quota", func() {
				// The route is registered as "GET /api/v1/providers/quota"
				// so a POST hits net/http's 405 path before the handler
				// sees the request. Defensive — confirms the route
				// pattern carries the GET prefix.
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota",
					strings.NewReader(`{}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusMethodNotAllowed),
					"POST against the list endpoint must not match the GET route — 405 from net/http's pattern matcher")
			})

			It("does not route GET /api/v1/providers/quota/reset to the reset handler", func() {
				// The reset route is registered with the POST method
				// prefix, so a GET against the same path does not match
				// it. net/http's behaviour here depends on whether
				// another route binds the same path — in this server
				// only POST does, so the GET either falls through to a
				// catch-all (SPA static handler, returning 302/200) or
				// hits 404/405. The load-bearing guarantee is the
				// reset handler does not run — the aggregator stays
				// uncalled. Drive that directly.
				req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/quota/reset", nil)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).NotTo(Equal(http.StatusOK),
					"GET against the reset endpoint must NOT successfully reset — handler must not run")
				Expect(aggregator.resetCalls).To(BeEmpty(),
					"reset handler MUST NOT be reachable via GET — the method-prefix route binding short-circuits before the handler")
			})
		})

		Context("POST /reset — body validation (DisallowUnknownFields)", func() {
			It("returns 400 invalid_request on malformed JSON", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{not json`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
				Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
			})

			It("returns 400 invalid_request when 'provider' is empty", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{"provider":"","account_hash":"a","model":"m"}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
				Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
			})

			It("returns 400 invalid_request when 'model' is empty", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{"provider":"anthropic","account_hash":"a","model":""}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest))
				Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
			})

			It("returns 400 invalid_request on unknown fields (DisallowUnknownFields strict-body discipline)", func() {
				// "extra" is not a declared field on quotaResetRequest;
				// the decoder rejects with an error before the handler
				// runs the empty-field check. Pins the strict-body
				// stance carried over from the Auth Track.
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{"provider":"anthropic","account_hash":"a","model":"m","extra":"nope"}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusBadRequest),
					"DisallowUnknownFields MUST reject unknown body fields with 400 — strict-body discipline per Auth Track")
				Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
			})

			It("accepts empty account_hash (v1 single-account-per-provider default) — body parses, handler proceeds to aggregator", func() {
				// Empty account_hash is the v1 default partition key for
				// single-account-per-provider deployments. The handler
				// must NOT reject with 400 on this shape; it threads
				// through to ResetQuotaSpend and surfaces whatever the
				// aggregator returns (here: (false, nil) → 404).
				aggregator.resetReturnFound = false
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{"provider":"anthropic","account_hash":"","model":"claude-opus-4-7"}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				Expect(rec.Code).To(Equal(http.StatusNotFound),
					"empty account_hash is valid; handler proceeds to aggregator which returns not-found → 404")
				Expect(aggregator.resetCalls).To(HaveLen(1))
				Expect(aggregator.resetCalls[0].accountHash).To(Equal(""))
			})
		})

		Context("POST /reset — aggregator response branches", func() {
			It("returns 404 not_found when the aggregator reports no snapshot for the key", func() {
				aggregator.resetReturnFound = false
				aggregator.resetReturnErr = nil
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{"provider":"anthropic","account_hash":"acc-A","model":"unknown-model"}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusNotFound),
					"aggregator returning (false, nil) MUST surface as 404 not_found — distinguishes 'no row' from internal failure")
				Expect(rec.Body.String()).To(ContainSubstring("not_found"))
			})

			It("returns 500 internal_error when the aggregator surfaces a Store error", func() {
				aggregator.resetReturnFound = false
				aggregator.resetReturnErr = errors.New("simulated store impl failure")
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{"provider":"anthropic","account_hash":"acc-A","model":"claude-opus-4-7"}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusInternalServerError),
					"aggregator returning a non-nil error MUST surface as 500 internal_error")
				Expect(rec.Body.String()).To(ContainSubstring("internal_error"))
			})

			It("returns 200 on the happy path AND threads the body fields into the aggregator", func() {
				aggregator.resetReturnFound = true
				aggregator.resetReturnErr = nil
				req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
					strings.NewReader(`{"provider":"anthropic","account_hash":"deadbeef1234","model":"claude-opus-4-7"}`))
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))
				Expect(rec.Header().Get("Content-Type")).To(Equal("application/json"))
				Expect(rec.Body.String()).To(ContainSubstring(`"status":"reset"`))

				// Body fields MUST thread through verbatim — the
				// partition key the dashboard surfaces is the partition
				// key the engine actually keys on.
				Expect(aggregator.resetCalls).To(HaveLen(1))
				Expect(aggregator.resetCalls[0]).To(Equal(resetCall{
					provider:    "anthropic",
					accountHash: "deadbeef1234",
					model:       "claude-opus-4-7",
				}))
			})
		})

		Context("GET — happy path projection", func() {
			It("returns 200 with empty JSON array when aggregator holds no rows", func() {
				aggregator.snapshots = nil
				req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/quota", nil)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK),
					"aggregator wired but empty MUST be 200 + [] — distinct from 501 (unwired)")
				Expect(rec.Header().Get("Content-Type")).To(Equal("application/json"))
				// Body is a JSON array, not null. The handler builds the
				// rows slice with make(..., 0, len(entries)) so the
				// marshalled output is "[]\n" rather than "null\n".
				Expect(strings.TrimSpace(rec.Body.String())).To(Equal("[]"))
			})

			It("returns 200 with a JSON array carrying the dashboard rows when the aggregator has entries", func() {
				now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
				aggregator.snapshots = []api.QuotaAggregatorRow{
					{
						Provider:    "anthropic",
						AccountHash: "deadbeef1234",
						Model:       "claude-opus-4-7",
						Snapshot: quota.Snapshot{
							Provider:     "anthropic",
							AccountHash:  "deadbeef1234",
							Model:        "claude-opus-4-7",
							ObservedAt:   now,
							StoreBackend: "memory",
							TokenSpend: &quota.TokenSpendVariant{
								Spent:          quota.Money{Amount: 350, Currency: "USD"},
								SpentUSD:       quota.Money{Amount: 350, Currency: "USD"},
								Cap:            quota.Money{Amount: 5000, Currency: "USD"},
								Period:         "monthly",
								PeriodStart:    now.AddDate(0, 0, -13),
								PeriodEnd:      now.AddDate(0, 0, 17),
								ThresholdAmber: 80,
								ThresholdRed:   95,
							},
						},
					},
				}
				req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/quota", nil)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))
				body := rec.Body.String()
				Expect(body).To(ContainSubstring(`"provider":"anthropic"`))
				Expect(body).To(ContainSubstring(`"account_hash":"deadbeef1234"`))
				Expect(body).To(ContainSubstring(`"model":"claude-opus-4-7"`))
				Expect(body).To(ContainSubstring(`"variant":"token_spend"`))
				Expect(body).To(ContainSubstring(`"spent_minor":350`))
				Expect(body).To(ContainSubstring(`"cap_minor":5000`))
				Expect(body).To(ContainSubstring(`"threshold_amber":80`))
				Expect(body).To(ContainSubstring(`"threshold_red":95`))
				// Outer container is a JSON array — the SPA deserialises
				// this with the same discriminated-union types it uses
				// for the SSE event, so the shape must be array-of-rows.
				Expect(strings.TrimSpace(body)).To(HavePrefix("["))
				Expect(strings.TrimSpace(body)).To(HaveSuffix("]"))
			})

			It("suppresses malformed snapshots (defensive — a single bad row must not blank the whole dashboard)", func() {
				now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
				aggregator.snapshots = []api.QuotaAggregatorRow{
					{
						// Discriminator invariant violation — both
						// RateLimit and TokenSpend set. Snapshot.IsValid()
						// returns false, the handler skips the row.
						Provider:    "anthropic",
						AccountHash: "acc-A",
						Model:       "claude-opus-4-7",
						Snapshot: quota.Snapshot{
							RateLimit:  &quota.RateLimitVariant{TightestPercentRemaining: 42},
							TokenSpend: &quota.TokenSpendVariant{Spent: quota.Money{Amount: 1, Currency: "USD"}},
						},
					},
					{
						Provider:    "openai",
						AccountHash: "acc-B",
						Model:       "gpt-4o",
						Snapshot: quota.Snapshot{
							Provider:    "openai",
							AccountHash: "acc-B",
							Model:       "gpt-4o",
							ObservedAt:  now,
							TokenSpend: &quota.TokenSpendVariant{
								Spent:    quota.Money{Amount: 200, Currency: "USD"},
								SpentUSD: quota.Money{Amount: 200, Currency: "USD"},
								Period:   "monthly",
							},
						},
					},
				}
				req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/quota", nil)
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusOK))
				body := rec.Body.String()
				// The valid row makes it through; the malformed one
				// is dropped silently (no 500 blanking the surface).
				Expect(body).To(ContainSubstring(`"provider":"openai"`))
				Expect(body).NotTo(ContainSubstring(`"provider":"anthropic"`))
			})
		})
	})

	Context("401 wire-shape parity with peer protected endpoints (B8 carry-through)", func() {
		// Auth Track PR3 B8 discipline: an unauthenticated request to
		// every protected endpoint MUST return the same 401 body
		// (`unauthenticated\n` from http.Error) so callers cannot
		// fingerprint which routes exist by probing 401 bodies. The
		// quota dashboard endpoints are registered via
		// registerProtected (server.go:618-619) so they inherit the
		// uniform shape from auth.Protected → RequireSession.
		//
		// Peer chosen: GET /api/v1/sessions (server.go:546). Both routes
		// go through registerProtected; the 401 body MUST be
		// byte-identical.
		var (
			srv        *api.Server
			aggregator *fakeQuotaAggregator
		)

		BeforeEach(func() {
			aggregator = &fakeQuotaAggregator{}
			srv = api.NewServer(nil, registry, nil, nil,
				api.WithAuth(newQuotaDashboardAuthBundle()),
				api.WithQuotaAggregator(aggregator),
			)
		})

		It("GET /api/v1/providers/quota without session returns 401 + uniform 'unauthenticated' body", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/quota", nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusUnauthorized),
				"protected endpoint MUST return 401 to unauthenticated callers — handler never runs")
			Expect(rec.Body.String()).To(ContainSubstring("unauthenticated"))
			// Handler must NOT have been reached — aggregator stays
			// unused on the 401 path.
			Expect(aggregator.resetCalls).To(BeEmpty())
		})

		It("POST /api/v1/providers/quota/reset without session returns 401 + uniform 'unauthenticated' body", func() {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
				strings.NewReader(`{"provider":"anthropic","account_hash":"a","model":"m"}`))
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusUnauthorized))
			Expect(rec.Body.String()).To(ContainSubstring("unauthenticated"))
			Expect(aggregator.resetCalls).To(BeEmpty(),
				"401 from RequireSession MUST short-circuit BEFORE the handler — aggregator must not be called")
		})

		It("returns BYTE-IDENTICAL 401 body to peer protected endpoint GET /api/v1/sessions (B8 wire-shape parity)", func() {
			// Drive both endpoints through the same server with the
			// same auth bundle. The 401 body must be the same bytes —
			// the SPA cannot distinguish "endpoint X exists" from
			// "endpoint Y exists" by inspecting the unauth response.
			//
			// Peer: GET /api/v1/sessions (server.go:546). Both go
			// through registerProtected → auth.Protected →
			// RequireSession → http.Error(w, "unauthenticated", 401).
			peerReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			peerRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(peerRec, peerReq)

			quotaReq := httptest.NewRequest(http.MethodGet, "/api/v1/providers/quota", nil)
			quotaRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(quotaRec, quotaReq)

			Expect(peerRec.Code).To(Equal(http.StatusUnauthorized),
				"peer endpoint also returns 401 to unauthenticated callers")
			Expect(quotaRec.Code).To(Equal(http.StatusUnauthorized))

			// Bytes match exactly — same middleware chain, same write.
			Expect(quotaRec.Body.Bytes()).To(Equal(peerRec.Body.Bytes()),
				"401 body MUST be byte-identical between peer protected endpoints — B8 wire-shape parity. Quota body=%q peer body=%q",
				quotaRec.Body.String(), peerRec.Body.String())

			// Defensive — neither body carries fingerprintable hints
			// (mode, feature names, etc.).
			body := quotaRec.Body.String()
			Expect(body).NotTo(ContainSubstring("quota"))
			Expect(body).NotTo(ContainSubstring("provider"))
			Expect(body).NotTo(ContainSubstring("aggregator"))
		})

		It("POST reset 401 body byte-identical to peer protected POST (POST /api/v1/sessions)", func() {
			// Cross-check the unsafe-method path: both endpoints go
			// through the same chain. Pin the body match here too so a
			// future divergence (e.g. one branch adding a custom body)
			// fails loud.
			peerReq := httptest.NewRequest(http.MethodPost, "/api/v1/sessions",
				bytes.NewReader([]byte(`{}`)))
			peerRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(peerRec, peerReq)

			resetReq := httptest.NewRequest(http.MethodPost, "/api/v1/providers/quota/reset",
				strings.NewReader(`{"provider":"a","account_hash":"b","model":"c"}`))
			resetRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(resetRec, resetReq)

			Expect(peerRec.Code).To(Equal(http.StatusUnauthorized))
			Expect(resetRec.Code).To(Equal(http.StatusUnauthorized))
			Expect(resetRec.Body.Bytes()).To(Equal(peerRec.Body.Bytes()),
				"401 body MUST be byte-identical on unsafe-method protected endpoints too")
		})
	})
})

// newQuotaDashboardAuthBundle constructs a flag-on AuthBundle for the
// PR5 dashboard's 401 parity specs. Mirrors newWhoamiAuthBundle
// (auth_whoami_test.go:162) — distinct only because we want the test
// helpers self-contained per file so spec ordering is independent.
func newQuotaDashboardAuthBundle() api.AuthBundle {
	return newWhoamiAuthBundle()
}
