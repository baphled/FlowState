package swarm_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("ExtGateRunner registry", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
	})

	It("RegisterExtGateFunc registers a Go function as a gate", func() {
		Expect(swarm.RegisterExtGateFunc("test-pass", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			return swarm.ExtGateResponse{Pass: true}, nil
		})).To(Succeed())

		runner, ok := swarm.LookupExtGate("test-pass")
		Expect(ok).To(BeTrue())
		Expect(runner).ToNot(BeNil())
	})

	It("RegisterExtGateFromManifest registers the subprocess runner", func() {
		Expect(swarm.RegisterExtGateFromManifest(gates.Manifest{
			Name: "echo-pass", Dir: testdataDir("echo-pass"), Exec: "./gate.sh", Timeout: time.Second,
		})).To(Succeed())

		runner, ok := swarm.LookupExtGate("echo-pass")
		Expect(ok).To(BeTrue())
		resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
			MemberID: "x", When: "post-member", Payload: json.RawMessage(`"hi"`),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Pass).To(BeTrue())
	})

	It("DispatchExt routes pass:false to a *GateError with Reason", func() {
		Expect(swarm.RegisterExtGateFunc("blocker", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			return swarm.ExtGateResponse{Pass: false, Reason: "blocked"}, nil
		})).To(Succeed())

		err := swarm.DispatchExt(context.Background(), "ext:blocker", swarm.ExtGateRequest{
			MemberID: "x", When: "post-member",
		})

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(Equal("blocked"))
	})

	// Regression — the 2026-05-18 user report surfaced a relevance-gate
	// failure formatted as `gate "" (ext:relevance-gate post-member
	// researcher) failed for member "researcher" in swarm "":
	// payload is not valid JSON`. The empty `gate ""` and `swarm ""`
	// slots show that DispatchExt was constructing *GateError without
	// the GateName and SwarmID fields populated even though the calling
	// dispatcher (MultiRunner.Run) had both in scope. The wrapper MUST
	// thread the caller-supplied gate name and swarm id onto the typed
	// error so log readers and the CLI failure surface can locate the
	// failing gate without parsing the descriptor.
	It("DispatchExt populates GateName and SwarmID from the request when wrapping pass:false into a *GateError", func() {
		Expect(swarm.RegisterExtGateFunc("blocker", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			return swarm.ExtGateResponse{Pass: false, Reason: "blocked"}, nil
		})).To(Succeed())

		err := swarm.DispatchExt(context.Background(), "ext:blocker", swarm.ExtGateRequest{
			GateName: "relevance", SwarmID: "a-team", MemberID: "researcher", When: "post-member",
		})

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.GateName).To(Equal("relevance"),
			"GateError.GateName MUST be threaded from ExtGateRequest so the formatted error names the failing gate")
		Expect(gateErr.SwarmID).To(Equal("a-team"),
			"GateError.SwarmID MUST be threaded from ExtGateRequest so the formatted error names the failing swarm")
		Expect(gateErr.Error()).To(ContainSubstring(`gate "relevance"`))
		Expect(gateErr.Error()).To(ContainSubstring(`swarm "a-team"`))
	})

	It("DispatchExt populates GateName and SwarmID on runner-error wraps too", func() {
		boom := errors.New("subprocess crashed")
		Expect(swarm.RegisterExtGateFunc("crasher2", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			return swarm.ExtGateResponse{}, boom
		})).To(Succeed())

		err := swarm.DispatchExt(context.Background(), "ext:crasher2", swarm.ExtGateRequest{
			GateName: "relevance", SwarmID: "a-team", MemberID: "researcher", When: "post-member",
		})

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.GateName).To(Equal("relevance"))
		Expect(gateErr.SwarmID).To(Equal("a-team"))
		Expect(gateErr.Cause).To(Equal(boom))
	})

	It("DispatchExt routes runner errors to *GateError.Cause", func() {
		boom := errors.New("subprocess crashed")
		Expect(swarm.RegisterExtGateFunc("crasher", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			return swarm.ExtGateResponse{}, boom
		})).To(Succeed())

		err := swarm.DispatchExt(context.Background(), "ext:crasher", swarm.ExtGateRequest{})

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Cause).To(Equal(boom))
	})

	It("subprocess runner enforces timeout", func() {
		if runtime.GOOS == "windows" {
			Skip("subprocess runner uses POSIX shell")
		}
		Expect(swarm.RegisterExtGateFromManifest(gates.Manifest{
			Name: "slow", Dir: testdataDir("slow"), Exec: "./gate.sh", Timeout: 100 * time.Millisecond,
		})).To(Succeed())

		runner, _ := swarm.LookupExtGate("slow")
		_, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{MemberID: "x"})
		Expect(err).To(HaveOccurred())
	})

	It("subprocess runner errors on malformed JSON output", func() {
		Expect(swarm.RegisterExtGateFromManifest(gates.Manifest{
			Name: "bad-json", Dir: testdataDir("bad-json"), Exec: "./gate.sh", Timeout: 5 * time.Second,
		})).To(Succeed())

		runner, _ := swarm.LookupExtGate("bad-json")
		_, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{MemberID: "x"})
		Expect(err).To(MatchError(ContainSubstring("decode gate response")))
	})

	It("subprocess runner errors when exec is non-zero exit", func() {
		Expect(swarm.RegisterExtGateFromManifest(gates.Manifest{
			Name: "exit-1", Dir: testdataDir("exit-1"), Exec: "./gate.sh", Timeout: 5 * time.Second,
		})).To(Succeed())

		runner, _ := swarm.LookupExtGate("exit-1")
		_, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{MemberID: "x"})
		Expect(err).To(MatchError(ContainSubstring("exited")))
	})

	It("rejects double registration of the same name", func() {
		Expect(swarm.RegisterExtGateFunc("dup", noopFunc)).To(Succeed())
		err := swarm.RegisterExtGateFunc("dup", noopFunc)
		Expect(err).To(MatchError(ContainSubstring("already registered")))
	})
})

var _ = Describe("Gate runner — ext: routing", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
		swarm.ClearSchemasForTest()
		Expect(swarm.SeedDefaultSchemas()).To(Succeed())
	})

	It("routes kind:ext:* to the registered ExtGateRunner", func() {
		Expect(swarm.RegisterExtGateFunc("blocker", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			return swarm.ExtGateResponse{Pass: false, Reason: "blocked"}, nil
		})).To(Succeed())

		err := swarm.RunGateForTest(context.Background(), swarm.GateSpec{
			Name: "g", Kind: "ext:blocker", When: "post-member", Target: "x",
		}, swarm.GateInput{MemberID: "x", Payload: []byte(`"p"`)})

		Expect(err).To(HaveOccurred())
		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(Equal("blocked"))
	})

	It("rejects an ext:* kind whose runner is not registered", func() {
		err := swarm.RunGateForTest(context.Background(), swarm.GateSpec{
			Name: "g", Kind: "ext:nope", When: "post", Target: "",
		}, swarm.GateInput{})

		Expect(err).To(MatchError(ContainSubstring("ext:nope")))
	})

	It("preserves the existing builtin path", func() {
		err := swarm.RunGateForTest(context.Background(), swarm.GateSpec{
			Name: "g", Kind: "builtin:result-schema", SchemaRef: "evidence-bundle-v1",
			When: "post-member", Target: "explorer",
		}, swarm.GateInput{MemberID: "explorer", Payload: []byte(`{"summary":"ok","findings":[]}`)})

		Expect(err).ToNot(HaveOccurred())
	})
})

// ExtGateRequest wire-format invariants.
//
// Regression — the 2026-05-18 user report (`gate
// "post-member-researcher-relevance" (ext:relevance-gate post-member
// researcher) failed for member "researcher" in swarm "a-team": payload
// is not valid JSON`) traced to a wire-format defect on the host side.
// Go's `encoding/json` marshals a `[]byte` field as a base64-encoded
// string, so an `ExtGateRequest{Payload: []byte(jsonObject)}` rendered
// on stdin as `{"payload":"eyJ0...="}` — a base64 string, not a JSON
// object. The seeded gate.py shipped to the user's runtime (~/.config/
// flowstate/gates/relevance-gate/gate.py, mtime 7 May 2026) does
// `json.loads` on the payload, fails, and emits the "payload is not
// valid JSON" reason without ever seeing the real composed object.
//
// The fix changes `ExtGateRequest.Payload` to `json.RawMessage` so the
// composed JSON bytes embed verbatim into the marshalled stdin —
// `{"payload":{"task_plan":...,"research":...}}`. The framework now
// owns the invariant that Payload bytes are valid JSON; this also
// retroactively unbreaks the stale seeded gate.py without a re-seed
// because the new wire format produces a parsed dict (not a string)
// and the stale code's `isinstance(payload, str)` branch never fires.
//
// These specs pin three invariants together so a regression in any
// surfaces as a precise failure rather than the opaque "payload is not
// valid JSON" the user originally hit:
//
//   - The marshalled ExtGateRequest embeds the payload verbatim (no
//     base64 transform observable in the JSON bytes a subprocess
//     receives on stdin).
//   - The empty/zero Payload marshals as JSON null (not as the
//     empty-string "" base64 produced).
//   - A live subprocessRunner round-trip drops a composed JSON object
//     into the subprocess as a parsed dict (probed via a shell gate
//     that echoes back its stdin).
var _ = Describe("ExtGateRequest wire format (host -> subprocess stdin)", func() {
	It("marshals Payload as verbatim JSON bytes — no base64 transform observable", func() {
		composed := []byte(`{"task_plan":"investigate","research":"on-topic"}`)
		req := swarm.ExtGateRequest{
			GateName: "relevance",
			SwarmID:  "a-team",
			MemberID: "researcher",
			When:     "post-member",
			Payload:  composed,
		}

		body, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())

		// Decode the wire shape and assert payload arrived as a parsed
		// JSON object. The Go `[]byte`-as-base64 behaviour would land
		// here as a string field instead.
		var wire struct {
			Payload json.RawMessage `json:"payload"`
		}
		Expect(json.Unmarshal(body, &wire)).To(Succeed())
		Expect(json.Valid(wire.Payload)).To(BeTrue(),
			"wire payload MUST be valid JSON, got %q", string(wire.Payload))

		var asObject map[string]any
		Expect(json.Unmarshal(wire.Payload, &asObject)).To(Succeed(),
			"wire payload MUST decode as a JSON object — base64 transform would land here as a string")
		Expect(asObject).To(HaveKeyWithValue("task_plan", "investigate"))
		Expect(asObject).To(HaveKeyWithValue("research", "on-topic"))
	})

	It("marshals a nil/empty Payload as JSON null rather than the empty base64 string", func() {
		req := swarm.ExtGateRequest{
			GateName: "relevance",
			MemberID: "researcher",
			When:     "post-member",
			// Payload omitted -> nil
		}

		body, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())

		// The base64 behaviour would emit `"payload":""` (empty string);
		// the RawMessage behaviour emits `"payload":null`. Either way it
		// MUST be valid JSON and decodable to a Go nil interface so the
		// subprocess can branch on absence cleanly.
		Expect(body).To(ContainSubstring(`"payload":null`),
			"empty Payload MUST marshal as JSON null, got %s", string(body))
	})

	It("subprocessRunner sends the composed JSON object to gate stdin as a parsed dict (not a base64 string)", func() {
		if runtime.GOOS == "windows" {
			Skip("subprocess runner uses POSIX shell")
		}

		// echo-stdin gate.sh forwards a JSON response whose `reason`
		// field carries the verbatim stdin payload-field shape so the
		// test can assert the wire-format invariant end-to-end. The
		// gate.sh exists under testdata/echo-stdin.
		Expect(swarm.RegisterExtGateFromManifest(gates.Manifest{
			Name:    "echo-stdin",
			Dir:     testdataDir("echo-stdin"),
			Exec:    "./gate.sh",
			Timeout: 5 * time.Second,
		})).To(Succeed())
		DeferCleanup(swarm.ResetExtGateRegistryForTest)

		runner, ok := swarm.LookupExtGate("echo-stdin")
		Expect(ok).To(BeTrue())

		composed := []byte(`{"task_plan":"investigate","research":"on-topic"}`)
		resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
			GateName: "relevance",
			SwarmID:  "a-team",
			MemberID: "researcher",
			When:     "post-member",
			Payload:  composed,
		})
		Expect(err).ToNot(HaveOccurred())

		// The echo gate returns pass:false with reason = stdin payload
		// field's type after `json.loads`. Expectation: "dict" (the
		// composed object decoded as a Python dict), NOT "str" (the
		// base64 transform).
		Expect(resp.Reason).To(Equal("dict"),
			"subprocess gate MUST see payload as a parsed JSON object; reason=%q indicates the host still base64-encodes []byte", resp.Reason)
	})
})

// ExtGateRequest legacy single-key composition.
//
// Regression — the legacy fallback path (gates.go gateInputFromArgs) reads
// the raw member-output bytes from the coord-store and assigns them to
// `GateInput.Payload`. After the wire-format change, those bytes are
// embedded verbatim into the marshalled stdin — but a member's output is
// typically markdown / prose, not JSON. The host MUST wrap those bytes
// into a valid JSON value (a JSON-encoded string when the raw bytes
// don't parse as JSON; the raw bytes verbatim when they do) before
// assigning to the request. Otherwise the marshalled stdin becomes
// malformed and the subprocess fails to parse it.
//
// These specs run alongside the multi-key composition specs in
// gates_test.go; the multi-key path already wraps via
// embedAsJSONValue (commit `0cb50144`), so the wrapping invariant is
// shared. The legacy path MUST follow the same rule post-fix.
var _ = Describe("ExtGateRequest legacy single-key composition (wire-format wrapping)", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
	})

	It("wraps non-JSON readMemberOutput bytes as a JSON-encoded string before forwarding to the gate", func() {
		// The captured request's marshalled bytes must be valid JSON
		// end-to-end. A subprocess gate doing `json.load(sys.stdin)`
		// on the marshalled request body MUST see a well-formed
		// document with a parseable payload field — even when the
		// underlying member-output bytes were prose / markdown.
		var got swarm.ExtGateRequest
		captureRunner := func(_ context.Context, req swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			got = req
			return swarm.ExtGateResponse{Pass: true}, nil
		}
		Expect(swarm.RegisterExtGateFuncWithInputs("legacy-gate", captureRunner, nil)).To(Succeed())

		store := newGateStore(map[string][]byte{
			// raw prose — not JSON.
			"a-team/researcher/output": []byte(`# Findings\n\nThe race lives in dispatch.go:142.`),
		})
		multi := swarm.NewMultiRunner()
		err := multi.Run(context.Background(), swarm.GateSpec{
			Name: "legacy", Kind: "ext:legacy-gate", When: swarm.LifecyclePostMember, Target: "researcher",
		}, swarm.GateArgs{
			SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
		})

		Expect(err).ToNot(HaveOccurred())

		// The captured Payload MUST be valid JSON (a JSON-encoded
		// string of the original prose). Marshal the full request and
		// assert it round-trips through json.Unmarshal without error —
		// that proves the wire shape is well-formed for a subprocess
		// runner to consume.
		Expect(json.Valid(got.Payload)).To(BeTrue(),
			"legacy single-key Payload MUST be valid JSON for the wire format; got %q", string(got.Payload))

		var asString string
		Expect(json.Unmarshal(got.Payload, &asString)).To(Succeed(),
			"non-JSON member-output bytes MUST embed as a JSON-encoded string")
		Expect(asString).To(ContainSubstring("dispatch.go:142"))
	})

	It("preserves JSON readMemberOutput bytes verbatim (parses as the same JSON value)", func() {
		var got swarm.ExtGateRequest
		captureRunner := func(_ context.Context, req swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
			got = req
			return swarm.ExtGateResponse{Pass: true}, nil
		}
		Expect(swarm.RegisterExtGateFuncWithInputs("legacy-gate", captureRunner, nil)).To(Succeed())

		store := newGateStore(map[string][]byte{
			"a-team/researcher/output": []byte(`{"summary":"hello"}`),
		})
		multi := swarm.NewMultiRunner()
		err := multi.Run(context.Background(), swarm.GateSpec{
			Name: "legacy", Kind: "ext:legacy-gate", When: swarm.LifecyclePostMember, Target: "researcher",
		}, swarm.GateArgs{
			SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(json.Valid(got.Payload)).To(BeTrue())

		// JSON in -> JSON out, semantically equal. We do not require
		// byte-identity (whitespace normalisation is acceptable) — only
		// that the value re-decodes to the same structure.
		var asObject map[string]any
		Expect(json.Unmarshal(got.Payload, &asObject)).To(Succeed())
		Expect(asObject).To(HaveKeyWithValue("summary", "hello"))
	})
})

func noopFunc(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
	return swarm.ExtGateResponse{Pass: true}, nil
}

func testdataDir(name string) string {
	abs, err := filepath.Abs(filepath.Join("..", "gates", "testdata", name))
	Expect(err).ToNot(HaveOccurred())
	return abs
}

// relevanceGateManifestPath resolves the path of the bundled
// relevance-gate manifest as it lives under
// internal/app/gates/relevance-gate/. The relevance-gate spec runs
// against the actual bundled gate.py rather than a synthetic stub so a
// regression in the gate executable (e.g. an accidental change to the
// pass/fail thresholds) surfaces in CI rather than at first dispatch.
func relevanceGateManifestPath() string {
	abs, err := filepath.Abs(filepath.Join("..", "app", "gates", "relevance-gate", "manifest.yml"))
	Expect(err).ToNot(HaveOccurred())
	return abs
}

var _ = Describe("ext:relevance-gate (bundled gate)", func() {
	// Behavioural specs for the bundled relevance-gate executable. The
	// gate validates that the researcher's output is on-topic for the
	// task plan via word-overlap scoring; pass:true above the policy
	// threshold (default 0.4) and pass:false otherwise. These specs run
	// the actual gate.py shipped under internal/app/gates/relevance-gate
	// (not a Go stub) so a regression in the executable's pass/fail
	// logic surfaces here rather than at first A-Team dispatch.
	//
	// The host-side composition path packs the coord-store inputs
	// declared on the manifest into a multi-key JSON payload before
	// invoking the gate — see commit `0cb50144`. These specs feed that
	// payload directly via swarm.ExtGateRequest{Payload: ...} so they
	// pin the gate-executable contract independent of the host
	// composition path (which has its own coverage in gate_policy_test).

	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
		manifest, err := gates.LoadManifest(relevanceGateManifestPath())
		Expect(err).NotTo(HaveOccurred())
		Expect(swarm.RegisterExtGateFromManifest(manifest)).To(Succeed())
	})

	It("passes when research keywords overlap the task plan above the threshold", func() {
		runner, ok := swarm.LookupExtGate("relevance-gate")
		Expect(ok).To(BeTrue())

		payload := []byte(`{"task_plan":"investigate database connection pooling tuning patterns","research":"investigate database connection pooling tuning patterns demonstrate that pool size scales with workload"}`)

		resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
			MemberID: "researcher", When: "post-member", Payload: payload,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Pass).To(BeTrue(), "expected pass for high-overlap research, got reason=%q", resp.Reason)
	})

	It("rejects off-topic research with a redirect signal embedded in the reason", func() {
		runner, ok := swarm.LookupExtGate("relevance-gate")
		Expect(ok).To(BeTrue())

		payload := []byte(`{"task_plan":"investigate database connection pooling tuning patterns","research":"baking sourdough requires patience flour water salt and time"}`)

		resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
			MemberID: "researcher", When: "post-member", Payload: payload,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Pass).To(BeFalse(), "expected fail for off-topic research")
		Expect(resp.Reason).To(ContainSubstring("below threshold"))
		Expect(resp.Reason).To(ContainSubstring("Research should cover"))
	})

	It("declares the multi-key inputs the host's composition path needs", func() {
		// LookupGateInputs is the registry-level entry point the
		// composeMultiKeyPayload code path consults at dispatch time.
		// Pinning the registered inputs from the bundled manifest
		// guarantees the multi-key payload shape downstream gate.py
		// expects ({"task_plan":..., "research":...}) cannot drift away
		// from what the gate executable actually parses.
		inputs, ok := swarm.LookupGateInputs("relevance-gate")
		Expect(ok).To(BeTrue())
		Expect(inputs).To(HaveLen(2))

		byName := map[string]gates.InputSpec{}
		for _, in := range inputs {
			byName[in.Name] = in
		}
		Expect(byName).To(HaveKey("task_plan"))
		Expect(byName).To(HaveKey("research"))
		Expect(byName["task_plan"].Member).To(Equal("coordinator"))
		Expect(byName["task_plan"].OutputKey).To(Equal("task-plan"))
		Expect(byName["research"].Member).To(Equal(gates.TargetPlaceholder))
		Expect(byName["research"].OutputKey).To(Equal("output"))
	})
})

// quorumGateManifestPath resolves the path of the bundled quorum-gate
// manifest as it lives under internal/app/gates/quorum-gate/. The
// quorum-gate spec runs against the actual bundled gate.py rather
// than a synthetic stub so a regression in the gate executable (e.g.
// an accidental change to the divergence rule) surfaces in CI rather
// than at first Board Room dispatch.
func quorumGateManifestPath() string {
	abs, err := filepath.Abs(filepath.Join("..", "app", "gates", "quorum-gate", "manifest.yml"))
	Expect(err).ToNot(HaveOccurred())
	return abs
}

var _ = Describe("ext:quorum-gate (bundled gate)", func() {
	// Behavioural specs for the bundled quorum-gate executable. The
	// gate enforces the Board Room swarm's adversarial-debate
	// contract: all five analyst positions (bull, bear, market,
	// financial, technical) must be present at dispatch time AND the
	// bull and bear analysts must reach divergent decisions. These
	// specs run the actual gate.py shipped under
	// internal/app/gates/quorum-gate/ so a regression in the
	// executable surfaces here, not at first Board Room dispatch.
	//
	// The host-side composition path packs the coord-store inputs
	// declared on the manifest into a flat multi-key JSON payload
	// before invoking the gate — see commit `0cb50144`. These specs
	// feed that composed payload directly via
	// swarm.ExtGateRequest{Payload: ...} so they pin the gate-
	// executable contract independent of the host composition path
	// (which has its own coverage in gates_test).

	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
		manifest, err := gates.LoadManifest(quorumGateManifestPath())
		Expect(err).NotTo(HaveOccurred())
		Expect(swarm.RegisterExtGateFromManifest(manifest)).To(Succeed())
	})

	It("passes when all five positions are present and bull diverges from bear", func() {
		runner, ok := swarm.LookupExtGate("quorum-gate")
		Expect(ok).To(BeTrue())

		// Composed payload mirrors what composeMultiKeyPayload emits:
		// the five logical input names at the top level, each value
		// itself a JSON object embedded verbatim. Bull and bear
		// reach different decisions ("buy" vs "sell") — the
		// adversarial-divergence contract holds.
		payload := []byte(`{` +
			`"bull":{"decision":"buy","thesis":"strong fundamentals"},` +
			`"bear":{"decision":"sell","thesis":"valuation stretched"},` +
			`"market":{"decision":"hold","thesis":"timing ambiguous"},` +
			`"financial":{"decision":"buy","thesis":"unit economics work"},` +
			`"technical":{"decision":"buy","thesis":"feasible at scale"}` +
			`}`)

		resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
			MemberID: "technical-analyst", When: "post-member", Payload: payload,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Pass).To(BeTrue(), "expected pass when all five positions present and bull≠bear, got reason=%q", resp.Reason)
	})

	It("rejects collapsed adversarial review when bull and bear converge", func() {
		runner, ok := swarm.LookupExtGate("quorum-gate")
		Expect(ok).To(BeTrue())

		// Bull and bear both recommend "buy" — adversarial review has
		// collapsed. The gate must refuse and surface a diagnostic
		// the operator can read.
		payload := []byte(`{` +
			`"bull":{"decision":"buy","thesis":"upside dominates"},` +
			`"bear":{"decision":"buy","thesis":"risks priced in"},` +
			`"market":{"decision":"buy","thesis":"market expanding"},` +
			`"financial":{"decision":"buy","thesis":"margins improving"},` +
			`"technical":{"decision":"buy","thesis":"team ships"}` +
			`}`)

		resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
			MemberID: "technical-analyst", When: "post-member", Payload: payload,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Pass).To(BeFalse(), "expected fail for converged bull/bear")
		Expect(resp.Reason).To(ContainSubstring("adversarial review collapsed"))
	})

	It("rejects the dispatch when an analyst position is missing", func() {
		runner, ok := swarm.LookupExtGate("quorum-gate")
		Expect(ok).To(BeTrue())

		// Only four positions present — the financial analyst's slot
		// is absent. The gate must name the missing analyst in the
		// reason so operators can see which slot failed without
		// reading the raw payload.
		payload := []byte(`{` +
			`"bull":{"decision":"buy"},` +
			`"bear":{"decision":"sell"},` +
			`"market":{"decision":"hold"},` +
			`"technical":{"decision":"buy"}` +
			`}`)

		resp, err := runner.Evaluate(context.Background(), swarm.ExtGateRequest{
			MemberID: "technical-analyst", When: "post-member", Payload: payload,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Pass).To(BeFalse(), "expected fail when an analyst position is missing")
		Expect(resp.Reason).To(ContainSubstring("financial"),
			"expected the missing analyst (financial) named in reason, got %q", resp.Reason)
	})

	It("declares the five-key inputs the host's composition path needs", func() {
		// LookupGateInputs is the registry-level entry point the
		// composeMultiKeyPayload code path consults at dispatch time.
		// Pinning the registered inputs from the bundled manifest
		// guarantees the flat multi-key payload shape the gate.py
		// expects ({"bull":..., "bear":..., ...}) cannot drift away
		// from what the gate executable actually parses.
		inputs, ok := swarm.LookupGateInputs("quorum-gate")
		Expect(ok).To(BeTrue())
		Expect(inputs).To(HaveLen(5))

		byName := map[string]gates.InputSpec{}
		for _, in := range inputs {
			byName[in.Name] = in
		}
		Expect(byName).To(HaveKey("bull"))
		Expect(byName).To(HaveKey("bear"))
		Expect(byName).To(HaveKey("market"))
		Expect(byName).To(HaveKey("financial"))
		Expect(byName).To(HaveKey("technical"))

		Expect(byName["bull"].Member).To(Equal("bull-analyst"))
		Expect(byName["bull"].OutputKey).To(Equal("output"))
		Expect(byName["bear"].Member).To(Equal("bear-analyst"))
		Expect(byName["bear"].OutputKey).To(Equal("output"))
		Expect(byName["market"].Member).To(Equal("market-analyst"))
		Expect(byName["market"].OutputKey).To(Equal("output"))
		Expect(byName["financial"].Member).To(Equal("financial-analyst"))
		Expect(byName["financial"].OutputKey).To(Equal("output"))
		Expect(byName["technical"].Member).To(Equal("technical-analyst"))
		Expect(byName["technical"].OutputKey).To(Equal("output"))
	})
})
