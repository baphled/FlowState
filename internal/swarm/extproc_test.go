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

func noopFunc(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
	return swarm.ExtGateResponse{Pass: true}, nil
}

func testdataDir(name string) string {
	abs, err := filepath.Abs(filepath.Join("..", "gates", "testdata", name))
	Expect(err).ToNot(HaveOccurred())
	return abs
}
