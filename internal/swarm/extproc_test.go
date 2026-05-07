package swarm_test

import (
	"context"
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
			MemberID: "x", When: "post-member", Payload: []byte("hi"),
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
		}, swarm.GateInput{MemberID: "x", Payload: []byte("p")})

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
