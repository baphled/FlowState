package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/skill"
)

// fakeCompactionController is the api-side test double for the new
// CompactionController interface. It records what calls landed and
// returns scripted responses so the endpoint specs can pin the wire
// shape without standing up a full engine.
type fakeCompactionController struct {
	mu sync.Mutex

	threshold       float64
	setThresholdErr error
	setThresholdCalls []float64

	compactNowSummary string
	compactNowFired   bool
	compactNowCalls   []string
}

func (f *fakeCompactionController) AutoCompactionThreshold() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threshold
}

func (f *fakeCompactionController) SetAutoCompactionThreshold(t float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setThresholdCalls = append(f.setThresholdCalls, t)
	if f.setThresholdErr != nil {
		return f.setThresholdErr
	}
	// Mirror the production validation (engine.SetAutoCompactionThreshold)
	// so the api-side handler's "controller errors → 400" path is
	// exercised end-to-end through the fake. Without this guard the
	// fake would accept every input and the 400 branch would never
	// fire under test.
	if t <= 0 || t > 1 {
		return errOutOfRangeThreshold
	}
	f.threshold = t
	return nil
}

// errOutOfRangeThreshold mirrors engine.SetAutoCompactionThreshold's
// out-of-range error so the api-side handler's 400-mapping branch is
// exercised end-to-end through the fake.
var errOutOfRangeThreshold = errFakeOutOfRange{}

type errFakeOutOfRange struct{}

func (errFakeOutOfRange) Error() string {
	return "compression: threshold must be in the (0.0, 1.0] interval"
}

func (f *fakeCompactionController) CompactNow(_ context.Context, sessionID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compactNowCalls = append(f.compactNowCalls, sessionID)
	return f.compactNowSummary, f.compactNowFired
}

func (f *fakeCompactionController) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.compactNowCalls))
	copy(out, f.compactNowCalls)
	return out
}

// newCompressionTestServer wires a minimal server with the
// CompactionController option installed. The other server options
// stay nil since the compression endpoints don't touch them.
func newCompressionTestServer(fake *fakeCompactionController) *api.Server {
	registry := agent.NewRegistry()
	disc := discovery.NewAgentDiscovery(nil)
	var skills []skill.Skill
	return api.NewServer(nil, registry, disc, skills,
		api.WithCompactionController(fake),
	)
}

// Deliverable 2 & 3 — compression-config + manual-compaction
// endpoint surface for the May 2026 context-accuracy bundle.
var _ = Describe("Compression endpoints", func() {
	Describe("GET /api/v1/config/compression", func() {
		It("returns the current auto-compaction threshold", func() {
			fake := &fakeCompactionController{threshold: 0.42}
			server := newCompressionTestServer(fake)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/config/compression", http.NoBody)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			var body struct {
				Threshold float64 `json:"threshold"`
			}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
			Expect(body.Threshold).To(BeNumerically("~", 0.42, 1e-9))
		})

		It("returns 501 when no CompactionController is wired", func() {
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			server := api.NewServer(nil, registry, disc, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/config/compression", http.NoBody)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusNotImplemented),
				"endpoint must clearly signal 'wired but disabled' when no controller is "+
					"installed so callers can fall back to the default without guessing")
		})
	})

	Describe("PATCH /api/v1/config/compression", func() {
		It("updates the threshold and surfaces the new value in the response", func() {
			fake := &fakeCompactionController{threshold: 0.75}
			server := newCompressionTestServer(fake)

			body := bytes.NewBufferString(`{"threshold": 0.55}`)
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/compression", body)
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			var resp struct {
				Threshold float64 `json:"threshold"`
			}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Threshold).To(BeNumerically("~", 0.55, 1e-9))
			Expect(fake.AutoCompactionThreshold()).To(BeNumerically("~", 0.55, 1e-9))
		})

		It("returns 400 when the threshold is outside (0, 1]", func() {
			fake := &fakeCompactionController{threshold: 0.75}
			server := newCompressionTestServer(fake)

			cases := []string{
				`{"threshold": 0}`,
				`{"threshold": -0.5}`,
				`{"threshold": 1.5}`,
			}
			for _, payload := range cases {
				req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/compression",
					bytes.NewBufferString(payload))
				req.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				server.Handler().ServeHTTP(recorder, req)
				Expect(recorder.Code).To(Equal(http.StatusBadRequest),
					"out-of-range threshold "+payload+" must produce 400")
			}
		})

		It("returns 400 on malformed JSON body", func() {
			fake := &fakeCompactionController{threshold: 0.75}
			server := newCompressionTestServer(fake)

			req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/compression",
				bytes.NewBufferString("not json"))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("POST /api/v1/sessions/{id}/compress", func() {
		It("invokes CompactNow on the wired controller and surfaces the fire/no-fire signal", func() {
			fake := &fakeCompactionController{
				compactNowSummary: "[auto-compacted summary]: {\"intent\":\"x\"}",
				compactNowFired:   true,
			}
			server := newCompressionTestServer(fake)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/abc/compress", http.NoBody)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(fake.calls()).To(Equal([]string{"abc"}),
				"handler must thread the URL path id into CompactNow")

			var resp struct {
				Fired   bool   `json:"fired"`
				Summary string `json:"summary"`
			}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Fired).To(BeTrue())
			Expect(resp.Summary).To(ContainSubstring("auto-compacted summary"))
		})

		It("returns fired=false with a clear discriminant when there is nothing to compact", func() {
			fake := &fakeCompactionController{compactNowFired: false}
			server := newCompressionTestServer(fake)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/empty/compress", http.NoBody)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			var resp struct {
				Fired bool `json:"fired"`
			}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Fired).To(BeFalse(),
				"the slash-command UI uses fired=false to surface a 'nothing to compact' "+
					"toast — this discriminant is the source of truth for that copy")
		})

		It("returns 501 when no CompactionController is wired", func() {
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			server := api.NewServer(nil, registry, disc, nil)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/x/compress", http.NoBody)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusNotImplemented))
		})
	})
})
