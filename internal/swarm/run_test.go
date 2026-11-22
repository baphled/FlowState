package swarm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("Swarm run loop", func() {
	var manifestYAML string

	BeforeEach(func() {
		manifestYAML = `
name: demo.swarm
scope: demo.swarm
packets:
  - id: p1
    scope: demo.swarm.plan
    task: plan the work
    source_sha256: 2aefd47f9b215903adbbd512cbe68cc7861d56a5b40c566181a4eaeb59f1cb56
  - id: p2
    scope: demo.swarm.build
    task: build it
    source_sha256: 08cf56f9c012cc638d3725b58595e284e17a264ca0aba3beb7dcb4fea31dbb19
    prerequisites: [p1]
gates:
  - name: go
    description: go build
    command: true
`
	})

	Describe("cancellation", func() {
		var (
			m        swarm.Manifest
			receipts *runReceipts
			ran      []string
			exec     swarm.GateExecutor
		)

		BeforeEach(func() {
			var parseErr error
			m, parseErr = swarm.ParseManifest([]byte(manifestYAML))
			if parseErr != nil {
				panic(parseErr)
			}
			m.Gates = []swarm.Gate{{Name: "one", Command: "one"}, {Name: "two", Command: "two"}}
			receipts = &runReceipts{}
			ran = nil
			exec = nil
		})

		runWith := func(ctx context.Context) (swarm.RunResult, error) {
			return swarm.Run(ctx, m, hashFor(), swarm.NewGateRunnerWith(exec), nil, receipts)
		}

		It("does not start a gate when the context is cancelled before the run", func() {
			exec = func(_ context.Context, name string, _ []string) error {
				ran = append(ran, name)
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = runWith(ctx)
			Expect(ran).To(BeEmpty())
		})

		It("does not admit packets once the context is cancelled", func() {
			exec = func(context.Context, string, []string) error { return nil }
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = runWith(ctx)
			Expect(receipts.admissions).To(BeEmpty())
		})

		It("reports the cancellation as the run error", func() {
			exec = func(context.Context, string, []string) error { return nil }
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, runErr := runWith(ctx)
			Expect(runErr).To(MatchError(context.Canceled))
		})

		It("does not accept a cancelled run", func() {
			exec = func(context.Context, string, []string) error { return nil }
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			res, _ := runWith(ctx)
			Expect(res.Gates.Accepted).To(BeFalse())
		})

		It("still records the gate decision of a cancelled run", func() {
			exec = func(context.Context, string, []string) error { return nil }
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = runWith(ctx)
			Expect(receipts.gateDecision.Accepted).To(BeFalse())
		})

		Context("when cancelled while the first gate runs", func() {
			var runErr error

			BeforeEach(func() {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				// The first gate completes, then the caller cancels.
				exec = func(_ context.Context, name string, _ []string) error {
					ran = append(ran, name)
					cancel()
					return nil
				}
				_, runErr = runWith(ctx)
			})

			It("does not start the second gate", func() {
				Expect(ran).To(Equal([]string{"one"}))
			})

			It("records the receipt of the gate that completed", func() {
				Expect(receipts.gateDecision.Outcomes).To(HaveLen(1))
			})

			It("records the receipt with a context that is not cancelled", func() {
				Expect(receipts.gatesCtxErr).NotTo(HaveOccurred())
			})

			It("reports the cancellation", func() {
				Expect(runErr).To(MatchError(context.Canceled))
			})
		})
	})

	Describe("cancellation cutting a gate or the run short", func() {
		var (
			m      swarm.Manifest
			ctx    context.Context
			cancel context.CancelFunc
		)

		BeforeEach(func() {
			var parseErr error
			m, parseErr = swarm.ParseManifest([]byte(manifestYAML))
			if parseErr != nil {
				panic(parseErr)
			}
			ctx, cancel = context.WithCancel(context.Background())
		})

		AfterEach(func() { cancel() })

		// killedGate cancels the run and fails like a killed command.
		killedGate := func(context.Context, string, []string) error {
			cancel()
			return errors.New("signal: killed")
		}

		It("reports the cancellation of a one-gate run cancelled mid-gate", func() {
			m.Gates = []swarm.Gate{{Name: "only", Command: "only"}}
			_, runErr := swarm.Run(ctx, m, hashFor(), swarm.NewGateRunnerWith(killedGate), nil, nil)
			Expect(runErr).To(MatchError(context.Canceled))
		})

		It("reports the cancellation when the last of two gates is cancelled mid-gate", func() {
			m.Gates = []swarm.Gate{{Name: "one", Command: "one"}, {Name: "two", Command: "two"}}
			exec := func(c context.Context, name string, args []string) error {
				if name == "one" {
					return nil
				}
				return killedGate(c, name, args)
			}
			_, runErr := swarm.Run(ctx, m, hashFor(), swarm.NewGateRunnerWith(exec), nil, nil)
			Expect(runErr).To(MatchError(context.Canceled))
		})

		Context("when every gate finished before the late cancellation", func() {
			var (
				res    swarm.RunResult
				runErr error
			)

			BeforeEach(func() {
				m.Gates = []swarm.Gate{{Name: "only", Command: "only"}}
				// The gate passes on its own; the caller cancels as it ends.
				exec := func(context.Context, string, []string) error {
					cancel()
					return nil
				}
				res, runErr = swarm.Run(ctx, m, hashFor(), swarm.NewGateRunnerWith(exec), nil, nil)
			})

			It("does not report the cancellation", func() {
				Expect(errors.Is(runErr, context.Canceled)).To(BeFalse())
			})

			It("keeps the real result", func() {
				Expect(res.Gates.Accepted).To(BeTrue())
			})
		})
	})

	Describe("admission recorded after the context is cancelled inside Admit", func() {
		var (
			m        swarm.Manifest
			receipts *runReceipts
			events   *runEvents
			admitter *cancellingAdmitter
		)

		BeforeEach(func() {
			var parseErr error
			m, parseErr = swarm.ParseManifest([]byte(manifestYAML))
			if parseErr != nil {
				panic(parseErr)
			}
			receipts, events = &runReceipts{}, &runEvents{}
			admitter = &cancellingAdmitter{}
		})

		run := func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			admitter.cancel = cancel
			_, _ = swarm.Run(ctx, m, admitter, swarm.NewGateRunnerWith(
				func(context.Context, string, []string) error { return nil }), events, receipts)
		}

		It("records an admission receipt with a context that is not cancelled", func() {
			run()
			Expect(receipts.admissionCtxErr).NotTo(HaveOccurred())
		})

		It("reports PacketAdmitted with a context that is not cancelled", func() {
			run()
			Expect(events.admittedCtxErr).NotTo(HaveOccurred())
		})

		It("records a rejection receipt with a context that is not cancelled", func() {
			admitter.err = errors.New("refused")
			run()
			Expect(receipts.admissionCtxErr).NotTo(HaveOccurred())
		})

		It("reports PacketRejected with a context that is not cancelled", func() {
			admitter.err = errors.New("refused")
			run()
			Expect(events.rejectedCtxErr).NotTo(HaveOccurred())
		})
	})

	It("loads and validates a YAML manifest", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "m.yaml")
		writeErr := os.WriteFile(path, []byte(manifestYAML), 0o600)
		Expect(writeErr).NotTo(HaveOccurred())
		m, _ := swarm.LoadManifest(path)
		Expect(m.Name).To(Equal("demo.swarm"))
	})

	It("admits every packet in prerequisite order", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		res, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, nil)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Admitted).To(Equal([]string{"p1", "p2"}))
	})

	It("accepts the run when every gate passes", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		res, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, nil)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Gates.Accepted).To(BeTrue())
	})
	It("rejects acceptance even if gates pass when admission was rejected", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		m.Packets[0].SourceSHA256 = swarm.SHA256HexForTest([]byte("incorrect-source"))
		res, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, nil)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Gates.Accepted).To(BeFalse())
	})

	It("records successful packet admissions before completing", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{}
		_, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(receipts.admissions).To(Equal([]bool{true, true}))
	})

	It("records packet rejections before gate evaluation", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		m.Packets[0].SourceSHA256 = swarm.SHA256HexForTest([]byte("incorrect-source"))
		receipts := &runReceipts{}
		_, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(receipts.admissions).To(Equal([]bool{false, false}))
	})

	It("fails the run if admission evidence cannot be persisted", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{admissionErr: errors.New("durable admission unavailable")}
		_, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).To(MatchError(ContainSubstring("durable admission unavailable")))
	})

	It("fails the run if rejection evidence cannot be persisted", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		m.Packets[0].SourceSHA256 = swarm.SHA256HexForTest([]byte("incorrect-source"))
		receipts := &runReceipts{admissionErr: errors.New("durable rejection unavailable")}
		_, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).To(MatchError(ContainSubstring("durable rejection unavailable")))
	})

	It("propagates failed gate evidence persistence", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{gatesErr: errors.New("durable gates unavailable")}
		_, runErr := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).To(MatchError(ContainSubstring("durable gates unavailable")))
	})

	It("revokes gate acceptance after evidence persistence fails", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{gatesErr: errors.New("durable gates unavailable")}
		res, _ := swarm.Run(context.Background(), m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(res.Gates.Accepted).To(BeFalse())
	})

	It("rejects missing admission collaborators instead of panicking", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		_, runErr := swarm.Run(context.Background(), m, nil, swarm.NewGateRunner(), nil, nil)
		Expect(runErr).To(HaveOccurred())
	})

})

type runReceipts struct {
	admissionErr error
	gatesErr     error
	admissions   []bool
	gateDecision swarm.GatesDecision
	gatesCtxErr  error

	admissionCtxErr error
}

// cancellingAdmitter cancels the run inside Admit and returns err.
type cancellingAdmitter struct {
	cancel func()
	err    error
}

func (a *cancellingAdmitter) Admit(swarm.Packet, []byte) error {
	a.cancel()
	return a.err
}

// runEvents records the cancellation state of each event context.
type runEvents struct {
	admittedCtxErr error
	rejectedCtxErr error
}

func (e *runEvents) PacketAdmitted(ctx context.Context, _ swarm.Packet) { e.admittedCtxErr = ctx.Err() }

func (e *runEvents) PacketRejected(ctx context.Context, _, _ string) { e.rejectedCtxErr = ctx.Err() }

func (r *runReceipts) RecordAdmission(ctx context.Context, _ string, _ swarm.Packet, admitted bool, _ string) error {
	r.admissionCtxErr = ctx.Err()
	r.admissions = append(r.admissions, admitted)
	return r.admissionErr
}

func (r *runReceipts) RecordGates(ctx context.Context, _ string, d swarm.GatesDecision) error {
	r.gateDecision, r.gatesCtxErr = d, ctx.Err()
	return r.gatesErr
}

// hashFor builds an admitter that computes each packet's source
// hash from its task text (the run loop's default body).
func hashFor() *hashedAdmitter {
	return &hashedAdmitter{log: swarm.NewAdmissionLog()}
}

type hashedAdmitter struct {
	log *swarm.AdmissionLog
}

func (h *hashedAdmitter) Admit(p swarm.Packet, body []byte) error {
	if p.SourceSHA256 == "body-hash" {
		p.SourceSHA256 = swarm.SHA256HexForTest(body)
	}
	return h.log.Admit(p, body)
}
