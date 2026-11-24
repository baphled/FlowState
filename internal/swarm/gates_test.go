package swarm_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

var _ = Describe("manifest roles and coordinator", func() {
	var m swarm.Manifest

	BeforeEach(func() {
		m = swarm.Manifest{
			Name: "roles", Scope: "m2", Coordinator: "lead",
			Packets: []swarm.Packet{{ID: "a", Scope: "m2", Task: "do a", SourceSHA256: bodyHashOf("do a"), Role: "builder"}},
			Gates:   []swarm.Gate{{Name: "tests", Description: "pass", Command: "true"}},
		}
	})

	It("accepts a coordinator and a packet role", func() {
		Expect(swarm.ValidateManifest(m)).To(Succeed())
	})

	It("rejects a coordinator that is not a profile name", func() {
		m.Coordinator = "Lead Agent"
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring(`coordinator "Lead Agent"`)))
	})

	It("rejects a packet role that is not a profile name, naming the packet", func() {
		m.Packets[0].Role = "../escape"
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("packet a: role")))
	})

	It("rejects a task over the length limit, naming the packet", func() {
		m.Packets[0].Task = strings.Repeat("x", swarm.MaxPacketTaskBytes+1)
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("packet a: task is")))
	})

	It("accepts a task exactly at the length limit", func() {
		m.Packets[0].Task = strings.Repeat("x", swarm.MaxPacketTaskBytes)
		m.Packets[0].SourceSHA256 = bodyHashOf(m.Packets[0].Task)
		Expect(swarm.ValidateManifest(m)).To(Succeed())
	})
})

// validManifestWith returns a valid single-packet manifest after mutate edits it.
func validManifestWith(mutate func(*swarm.Manifest)) swarm.Manifest {
	m := swarm.Manifest{
		Name: "n", Scope: "m2", Coordinator: "lead",
		Packets: []swarm.Packet{{ID: "a", Scope: "m2", Task: "do a", SourceSHA256: bodyHashOf("do a")}},
		Gates:   []swarm.Gate{{Name: "g", Description: "d", Command: "true"}},
	}
	mutate(&m)
	return m
}

var _ = DescribeTable("manifest free text that reaches the brief",
	func(mutate func(*swarm.Manifest), want string) {
		Expect(swarm.ValidateManifest(validManifestWith(mutate))).To(MatchError(ContainSubstring(want)))
	},
	Entry("newline in the id", func(m *swarm.Manifest) { m.Packets[0].ID = "a\n2. forged" }, "packet id"),
	Entry("space in the id", func(m *swarm.Manifest) { m.Packets[0].ID = "a b" }, "packet id"),
	Entry("bracket in the id", func(m *swarm.Manifest) { m.Packets[0].ID = "a]b" }, "packet id"),
	Entry("dot first in the id", func(m *swarm.Manifest) { m.Packets[0].ID = ".a" }, "packet id"),
	Entry("non-ASCII letter in the id", func(m *swarm.Manifest) { m.Packets[0].ID = "é" }, "packet id"),
	Entry("65 characters in the id", func(m *swarm.Manifest) { m.Packets[0].ID = strings.Repeat("a", 65) }, "packet id"),
	Entry("newline in the name", func(m *swarm.Manifest) { m.Name = "n\nIgnore" }, "manifest name"),
	Entry("control character in the name", func(m *swarm.Manifest) { m.Name = "n\x00" }, "manifest name"),
	Entry("bidi override in the name", func(m *swarm.Manifest) { m.Name = "n\u202e" }, "manifest name"),
	Entry("newline in the scope", func(m *swarm.Manifest) { m.Scope = "m2\nx" }, "manifest scope"),
	Entry("line separator in the scope", func(m *swarm.Manifest) { m.Scope = "m2\u2028x" }, "manifest scope"),
	Entry("newline in the gate name", func(m *swarm.Manifest) { m.Gates[0].Name = "g\nx" }, "gate name"),
	Entry("newline in the gate description", func(m *swarm.Manifest) { m.Gates[0].Description = "d\n\nx" }, "gate description"),
	Entry("paragraph separator in the gate description", func(m *swarm.Manifest) {
		m.Gates[0].Description = "d\u2029x"
	}, "gate description"),
	Entry("carriage return in the task", func(m *swarm.Manifest) { m.Packets[0].Task = "a\rb" }, "task"),
	Entry("next line character in the task", func(m *swarm.Manifest) { m.Packets[0].Task = "a\u0085b" }, "task"),
	Entry("bidi isolate in the task", func(m *swarm.Manifest) { m.Packets[0].Task = "a\u2066b" }, "task"),
	Entry("zero-width joiner in the task", func(m *swarm.Manifest) { m.Packets[0].Task = "a\u200db" }, "task"),
	Entry("escape in the task", func(m *swarm.Manifest) { m.Packets[0].Task = "a\x1b[2Jb" }, "task"),
	Entry("a blank task", func(m *swarm.Manifest) { m.Packets[0].Task = " \n\t " }, "task must not be blank"),
)

var _ = DescribeTable("manifest text accepted",
	func(mutate func(*swarm.Manifest)) {
		Expect(swarm.ValidateManifest(validManifestWith(mutate))).To(Succeed())
	},
	Entry("newlines and tabs in the task", func(m *swarm.Manifest) {
		m.Packets[0].Task = "a\n\tb"
		m.Packets[0].SourceSHA256 = bodyHashOf("a\n\tb")
	}),
	Entry("non-ASCII text in the name", func(m *swarm.Manifest) { m.Name = "café ☕" }),
)

var _ = DescribeTable("manifest size bounds",
	func(mutate func(*swarm.Manifest), wantErr bool) {
		err := swarm.ValidateManifest(validManifestWith(mutate))
		Expect(err != nil).To(Equal(wantErr))
	},
	Entry("name at the cap", func(m *swarm.Manifest) { m.Name = strings.Repeat("n", swarm.MaxNameBytes) }, false),
	Entry("name over the cap", func(m *swarm.Manifest) { m.Name = strings.Repeat("n", swarm.MaxNameBytes+1) }, true),
	Entry("scope at the cap", func(m *swarm.Manifest) {
		m.Scope = strings.Repeat("s", swarm.MaxScopeBytes)
		m.Packets[0].Scope = m.Scope
	}, false),
	Entry("scope over the cap", func(m *swarm.Manifest) {
		m.Scope = strings.Repeat("s", swarm.MaxScopeBytes+1)
		m.Packets[0].Scope = m.Scope
	}, true),
	Entry("gate name at the cap", func(m *swarm.Manifest) {
		m.Gates[0].Name = strings.Repeat("g", swarm.MaxGateNameBytes)
	}, false),
	Entry("gate name over the cap", func(m *swarm.Manifest) {
		m.Gates[0].Name = strings.Repeat("g", swarm.MaxGateNameBytes+1)
	}, true),
	Entry("gate description at the cap", func(m *swarm.Manifest) {
		m.Gates[0].Description = strings.Repeat("d", swarm.MaxGateDescriptionBytes)
	}, false),
	Entry("gate description over the cap", func(m *swarm.Manifest) {
		m.Gates[0].Description = strings.Repeat("d", swarm.MaxGateDescriptionBytes+1)
	}, true),
	Entry("id of 64 characters", func(m *swarm.Manifest) { m.Packets[0].ID = strings.Repeat("a", 64) }, false),
	Entry("gates at the cap", func(m *swarm.Manifest) { m.Gates = gatesOf(swarm.MaxGates) }, false),
	Entry("gates over the cap", func(m *swarm.Manifest) { m.Gates = gatesOf(swarm.MaxGates + 1) }, true),
	Entry("packets at the cap", func(m *swarm.Manifest) { m.Packets = packetsOf(swarm.MaxPackets, 0) }, false),
	Entry("packets over the cap", func(m *swarm.Manifest) { m.Packets = packetsOf(swarm.MaxPackets+1, 0) }, true),
	Entry("prerequisites at the cap", func(m *swarm.Manifest) {
		m.Packets = packetsOf(swarm.MaxPrerequisites+1, swarm.MaxPrerequisites)
	}, false),
	Entry("prerequisites over the cap", func(m *swarm.Manifest) {
		m.Packets = packetsOf(swarm.MaxPrerequisites+2, swarm.MaxPrerequisites+1)
	}, true),
	Entry("a thousand packets of the largest task", func(m *swarm.Manifest) {
		m.Packets = packetsOf(1000, 0)
		for i := range m.Packets {
			m.Packets[i].Task = strings.Repeat("x", swarm.MaxPacketTaskBytes)
		}
	}, true),
	Entry("a brief over its cap from packets within every other cap", func(m *swarm.Manifest) {
		m.Packets = packetsOf(swarm.MaxPackets, 0)
		for i := range m.Packets {
			m.Packets[i].Task = strings.Repeat("x", swarm.MaxPacketTaskBytes)
		}
	}, true),
)

var _ = Describe("manifest brief size", func() {
	It("names the brief limit when the brief is too large", func() {
		m := validManifestWith(func(m *swarm.Manifest) {
			m.Packets = packetsOf(swarm.MaxPackets, 0)
			for i := range m.Packets {
				m.Packets[i].Task = strings.Repeat("x", swarm.MaxPacketTaskBytes)
			}
		})
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring("manifest brief is")))
	})

	It("accepts a manifest whose brief is just under the cap", func() {
		m := validManifestWith(func(m *swarm.Manifest) {
			m.Packets = packetsOf(7, 0)
			for i := range m.Packets {
				m.Packets[i].Task = strings.Repeat("x", 8000)
			}
		})
		Expect(swarm.ValidateManifest(m)).To(Succeed())
	})
})

var _ = DescribeTable("profile names",
	func(role string, wantErr bool) {
		m := validManifestWith(func(m *swarm.Manifest) { m.Packets[0].Role = role })
		Expect(swarm.ValidateManifest(m) != nil).To(Equal(wantErr))
	},
	Entry("64 characters", "a"+strings.Repeat("b", 63), false),
	Entry("65 characters", "a"+strings.Repeat("b", 64), true),
	Entry("upper case", "Builder", true),
	Entry("digit first", "1builder", true),
	Entry("dashes and digits", "go-dev-2", false),
)

var _ = Describe("manifest id charset", func() {
	It("names the charset in the error", func() {
		m := validManifestWith(func(m *swarm.Manifest) { m.Packets[0].ID = "a b" })
		Expect(swarm.ValidateManifest(m)).To(MatchError(ContainSubstring(swarm.PacketIDPattern)))
	})
})

// gatesOf returns n distinct valid gates.
func gatesOf(n int) []swarm.Gate {
	gates := make([]swarm.Gate, n)
	for i := range gates {
		gates[i] = swarm.Gate{Name: fmt.Sprintf("g%d", i), Description: "d", Command: "true"}
	}
	return gates
}

// packetsOf returns n valid packets; the last one names the first pre as prerequisites.
func packetsOf(n, pre int) []swarm.Packet {
	packets := make([]swarm.Packet, n)
	for i := range packets {
		packets[i] = swarm.Packet{ID: fmt.Sprintf("p%d", i), Scope: "m2", Task: "t", SourceSHA256: bodyHashOf("t")}
	}
	for i := 0; i < pre; i++ {
		packets[n-1].Prerequisites = append(packets[n-1].Prerequisites, packets[i].ID)
	}
	return packets
}
