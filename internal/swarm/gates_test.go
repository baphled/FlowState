package swarm_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("configured swarm manifest and gates", func() {
	// validManifest is a consistent manifest fixture (fixture setup).
	validManifest := func() swarm.Manifest {
		return swarm.Manifest{
			Name:  "docs-swarm",
			Scope: "m2",
			Packets: []swarm.Packet{
				{ID: "root", Scope: "m2", Task: "root work", SourceSHA256: bodyHashOf("root-src")},
			},
			Gates: []swarm.Gate{
				{Name: "tests", Description: "unit tests pass", Command: "true"},
			},
		}
	}

	It("accepts a consistent manifest with packet inside scope and named gates", func() {
		Expect(swarm.ValidateManifest(validManifest())).To(Succeed())
	})

	It("rejects a manifest without a name", func() {
		m := validManifest()
		m.Name = " "
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("name is required")))
	})

	It("rejects a manifest without a scope", func() {
		m := validManifest()
		m.Scope = ""
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("scope")))
	})

	It("rejects duplicate packet ids", func() {
		m := validManifest()
		m.Packets = append(m.Packets, swarm.Packet{ID: "root", Scope: "m2.runtime", Task: "dup", SourceSHA256: bodyHashOf("x")})
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("duplicate packet id")))
	})

	It("rejects a packet whose scope escapes the manifest scope", func() {
		m := validManifest()
		m.Packets[0].Scope = "other.scope"
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("escapes manifest scope")))
	})

	It("rejects an invalid packet inside an otherwise valid manifest", func() {
		m := validManifest()
		m.Packets[0].SourceSHA256 = "not-a-hash"
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("source_sha256")))
	})

	It("rejects unknown prerequisites before scheduling work", func() {
		m := validManifest()
		m.Packets[0].Prerequisites = []string{"missing"}
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("unknown prerequisite")))
	})

	It("rejects repeated prerequisite edges", func() {
		m := validManifest()
		child := swarm.Packet{ID: "child", Scope: "m2.child", Task: "child work",
			SourceSHA256: bodyHashOf("child"), Prerequisites: []string{"root", "root"}}
		m.Packets = append(m.Packets, child)
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("duplicate prerequisite")))
	})

	It("rejects cycles in manifest prerequisites", func() {
		m := validManifest()
		m.Packets[0].Prerequisites = []string{"child"}
		child := swarm.Packet{ID: "child", Scope: "m2.child", Task: "child work",
			SourceSHA256: bodyHashOf("child"), Prerequisites: []string{"root"}}
		m.Packets = append(m.Packets, child)
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("prerequisite cycle")))
	})

	It("accepts a valid two-packet dependency chain", func() {
		m := validManifest()
		child := swarm.Packet{ID: "child", Scope: "m2.child", Task: "child work",
			SourceSHA256: bodyHashOf("child"), Prerequisites: []string{"root"}}
		m.Packets = append(m.Packets, child)
		Expect(swarm.ValidateManifest(m)).To(Succeed())
	})

	It("rejects a gate without a command", func() {
		m := validManifest()
		m.Gates[0].Command = ""
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("command is required")))
	})

	It("rejects duplicate gate names", func() {
		m := validManifest()
		m.Gates = append(m.Gates, swarm.Gate{Name: "tests", Command: "true"})
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("duplicate gate name")))
	})
})

var _ = Describe("gate runner decisions", func() {
	// manifestWithGates builds a manifest whose gates run the given
	// commands (fixture setup).
	manifestWithGates := func(commands ...string) swarm.Manifest {
		gates := []swarm.Gate{}
		for i, c := range commands {
			gates = append(gates, swarm.Gate{Name: string(rune('a' + i)), Command: c})
		}
		return swarm.Manifest{Name: "s", Scope: "m2", Gates: gates}
	}

	It("accepts when every gate command passes", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates("true", "true"))
		Expect(d.Accepted).To(BeTrue())
	})

	It("reports no failed gates when every gate passes", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates("true", "true"))
		Expect(d.FailedGateNames()).To(BeEmpty())
	})

	It("rejects when any gate command fails", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates("true", "false", "true"))
		Expect(d.Accepted).To(BeFalse())
	})

	It("names the failed gates when one gate fails", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates("true", "false", "true"))
		Expect(d.FailedGateNames()).To(Equal([]string{"b"}))
	})

	It("keeps per-gate outcomes in manifest order", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates("true", "false"))
		Expect(d.Outcomes[1].Gate.Name).To(Equal("b"))
	})

	It("marks a passing gate outcome as passed", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates("true"))
		Expect(d.Outcomes[0].Passed).To(BeTrue())
	})

	It("fails closed when the manifest declares no gates", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates())
		Expect(d.Accepted).To(BeFalse())
	})

	It("records no outcomes when the manifest declares no gates", func() {
		d := swarm.NewGateRunner().Evaluate(context.Background(), manifestWithGates())
		Expect(d.Outcomes).To(BeEmpty())
	})
})

var _ = Describe("gate runner cancellation", func() {
	var (
		started chan string
		m       swarm.Manifest
		exec    swarm.GateExecutor
	)

	BeforeEach(func() {
		started = make(chan string, 4)
		m = swarm.Manifest{Name: "s", Scope: "m2", Gates: []swarm.Gate{
			{Name: "first", Command: "first"}, {Name: "second", Command: "second"},
		}}
		// The fake gate command blocks until its context is cancelled.
		exec = func(ctx context.Context, name string, _ []string) error {
			started <- name
			<-ctx.Done()
			return ctx.Err()
		}
	})

	It("does not start a gate when the context is already cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		swarm.NewGateRunnerWith(exec).Evaluate(ctx, m)
		Expect(started).To(BeEmpty())
	})

	It("does not accept a decision that never started its gates", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(swarm.NewGateRunnerWith(exec).Evaluate(ctx, m).Accepted).To(BeFalse())
	})

	Context("when cancelled while the first gate runs", func() {
		var decision swarm.GatesDecision

		BeforeEach(func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan swarm.GatesDecision, 1)
			go func() { done <- swarm.NewGateRunnerWith(exec).Evaluate(ctx, m) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				panic("the first gate never started")
			}
			cancel()
			select {
			case decision = <-done:
			case <-time.After(5 * time.Second):
				panic("the running gate command was not stopped")
			}
		})

		It("stops the running gate command", func() {
			Expect(decision.Outcomes).To(HaveLen(1))
		})

		It("does not start the next gate", func() {
			Expect(started).To(BeEmpty())
		})

		It("flags the interrupted gate", func() {
			Expect(decision.Outcomes[0].Interrupted).To(BeTrue())
		})

		It("flags the decision as interrupted", func() {
			Expect(decision.Interrupted).To(BeTrue())
		})
	})

	It("does not flag a gate that failed on its own", func() {
		failing := func(context.Context, string, []string) error { return errors.New("exit status 1") }
		decision := swarm.NewGateRunnerWith(failing).Evaluate(context.Background(), m)
		Expect(decision.Interrupted).To(BeFalse())
	})

	It("does not flag a gate that passed although the context was cancelled meanwhile", func() {
		ctx, cancel := context.WithCancel(context.Background())
		passing := func(context.Context, string, []string) error {
			cancel()
			return nil
		}
		one := swarm.Manifest{Name: "s", Scope: "m2", Gates: m.Gates[:1]}
		Expect(swarm.NewGateRunnerWith(passing).Evaluate(ctx, one).Interrupted).To(BeFalse())
	})

	It("flags the decision as interrupted when a gate is skipped", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(swarm.NewGateRunnerWith(exec).Evaluate(ctx, m).Interrupted).To(BeTrue())
	})

	Context("when the real executor runs a long gate command", func() {
		var decision swarm.GatesDecision

		BeforeEach(func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			time.AfterFunc(200*time.Millisecond, cancel)
			done := make(chan swarm.GatesDecision, 1)
			long := swarm.Manifest{Name: "s", Scope: "m2", Gates: []swarm.Gate{{Name: "long", Command: "sleep 30"}}}
			go func() { done <- swarm.NewGateRunner().Evaluate(ctx, long) }()
			select {
			case decision = <-done:
			case <-time.After(10 * time.Second):
				panic("the real gate command was not stopped by cancellation")
			}
		})

		It("does not accept the cancelled gate", func() {
			Expect(decision.Accepted).To(BeFalse())
		})
	})
})
