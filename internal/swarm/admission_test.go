package swarm_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

func TestSwarm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Swarm Suite")
}

// bodyHashOf is the hex sha256 of body (fixture helper).
func bodyHashOf(body string) string {
	return swarm.SHA256HexForTest([]byte(body))
}

// rootPacket is a valid admitted-root fixture (fixture setup).
func rootPacket() swarm.Packet {
	return swarm.Packet{ID: "root", Scope: "m2", Task: "root work", SourceSHA256: bodyHashOf("root-src")}
}

// childOf is a valid child packet under parent (fixture setup).
func childOf(parent, scope string) swarm.Packet {
	return swarm.Packet{ID: "child-" + scope, Scope: scope, Task: "child work",
		ParentID: parent, SourceSHA256: bodyHashOf(scope)}
}

// admit is the fixture admission for setup graphs.
type admitFixture struct{ log *swarm.AdmissionLog }

// admits p as fixture setup, panicking on failure (never the spec's
// behavioural expectation).
func (f admitFixture) admits(p swarm.Packet, body string) {
	if err := f.log.Admit(p, []byte(body)); err != nil {
		panic("fixture admission failed: " + err.Error())
	}
}

var _ = Describe("AdmissionLog", func() {
	It("admits a structurally valid root packet", func() {
		log := swarm.NewAdmissionLog()
		Expect(log.Admit(rootPacket(), []byte("root-src"))).To(Succeed())
	})

	It("rejects a packet whose source body hash mismatches", func() {
		log := swarm.NewAdmissionLog()

		err := log.Admit(rootPacket(), []byte("tampered"))

		Expect(err).To(MatchError(swarm.ErrHashMismatch))
	})

	It("rejects admission with an unadmitted prerequisite", func() {
		log := swarm.NewAdmissionLog()
		p := rootPacket()
		p.Prerequisites = []string{"missing"}

		err := log.Admit(p, []byte("root-src"))

		Expect(err).To(MatchError(swarm.ErrPrerequisite))
	})

	It("rejects a child that widens its parent's scope", func() {
		log := swarm.NewAdmissionLog()
		parent := swarm.Packet{ID: "parent", Scope: "m2.runtime",
			Task: "parent work", SourceSHA256: bodyHashOf("parent-source")}
		admitErr := log.Admit(parent, []byte("parent-source"))
		Expect(admitErr).NotTo(HaveOccurred())

		child := childOf("parent", "m2")
		err := log.Admit(child, []byte("m2"))

		Expect(err).To(MatchError(swarm.ErrScopeEscape))
	})

	It("rejects a child scope escaping its parent scope", func() {
		log := swarm.NewAdmissionLog()
		admitFixture{log}.admits(rootPacket(), "root-src")

		err := log.Admit(childOf("root", "other.scope"), []byte("other.scope"))

		Expect(err).To(MatchError(swarm.ErrScopeEscape))
	})

	It("rejects a parent edge that closes a cycle", func() {
		log := swarm.NewAdmissionLog()
		admitFixture{log}.admits(rootPacket(), "root-src")
		a := childOf("root", "m2.a")
		admitFixture{log}.admits(a, "m2.a")
		b := childOf(a.ID, "m2.a.b")
		admitFixture{log}.admits(b, "m2.a.b")
		c := childOf(b.ID, "m2.a.b.c")
		admitFixture{log}.admits(c, "m2.a.b.c")

		err := log.Admit(swarm.Packet{ID: "root", Scope: "m2", Task: "root work",
			ParentID: c.ID, SourceSHA256: bodyHashOf("cycle")}, []byte("cycle"))

		Expect(err).To(MatchError(swarm.ErrCycle))
	})

	It("admits a prerequisite-satisfying chain in order", func() {
		log := swarm.NewAdmissionLog()
		admitFixture{log}.admits(rootPacket(), "root-src")
		p := childOf("root", "m2.child")
		p.Prerequisites = []string{"root"}

		admitFixture{log}.admits(p, "m2.child")

		Expect(log.Admitted(p.ID)).To(BeTrue())
	})

	It("rejects an invalid digest shape", func() {
		log := swarm.NewAdmissionLog()
		p := rootPacket()
		p.SourceSHA256 = "nothex"

		err := log.Admit(p, []byte("root-src"))

		Expect(err).To(MatchError(swarm.ErrInvalidPacket))
	})

	It("reports admitted packets only after admission", func() {
		log := swarm.NewAdmissionLog()

		Expect(log.Admitted("root")).To(BeFalse())
	})
})
