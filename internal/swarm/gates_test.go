package swarm_test

import (
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
		d := swarm.NewGateRunner().Evaluate(manifestWithGates("true", "true"))
		Expect(d.Accepted).To(BeTrue())
	})

	It("reports no failed gates when every gate passes", func() {
		d := swarm.NewGateRunner().Evaluate(manifestWithGates("true", "true"))
		Expect(d.FailedGateNames()).To(BeEmpty())
	})

	It("rejects when any gate command fails", func() {
		d := swarm.NewGateRunner().Evaluate(manifestWithGates("true", "false", "true"))
		Expect(d.Accepted).To(BeFalse())
	})

	It("names the failed gates when one gate fails", func() {
		d := swarm.NewGateRunner().Evaluate(manifestWithGates("true", "false", "true"))
		Expect(d.FailedGateNames()).To(Equal([]string{"b"}))
	})

	It("keeps per-gate outcomes in manifest order", func() {
		d := swarm.NewGateRunner().Evaluate(manifestWithGates("true", "false"))
		Expect(d.Outcomes[1].Gate.Name).To(Equal("b"))
	})

	It("marks a passing gate outcome as passed", func() {
		d := swarm.NewGateRunner().Evaluate(manifestWithGates("true"))
		Expect(d.Outcomes[0].Passed).To(BeTrue())
	})

	It("fails closed when the manifest declares no gates", func() {
		d := swarm.NewGateRunner().Evaluate(manifestWithGates())
		Expect(d.Accepted).To(BeFalse())
	})

	It("records no outcomes when the manifest declares no gates", func() {
		d := swarm.NewGateRunner().Evaluate(manifestWithGates())
		Expect(d.Outcomes).To(BeEmpty())
	})
})
