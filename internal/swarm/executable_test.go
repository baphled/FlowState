package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("CheckExecutable", func() {
	var m swarm.Manifest

	BeforeEach(func() {
		m = swarm.Manifest{Name: "demo", Scope: "m2", Coordinator: "lead", Packets: []swarm.Packet{
			{ID: "a", Role: "builder"}, {ID: "b"},
		}}
	})

	It("accepts a manifest whose coordinator and roles are loaded", func() {
		Expect(swarm.CheckExecutable(m, []string{"lead", "builder"})).To(Succeed())
	})

	It("refuses a manifest without a coordinator, naming the fix", func() {
		m.Coordinator = ""
		Expect(swarm.CheckExecutable(m, []string{"lead", "builder"})).To(MatchError(ContainSubstring("add `coordinator:")))
	})

	It("marks a missing coordinator as ErrNotExecutable", func() {
		m.Coordinator = ""
		Expect(swarm.CheckExecutable(m, nil)).To(MatchError(swarm.ErrNotExecutable))
	})

	It("marks a coordinator that is not loaded as ErrNotExecutable", func() {
		Expect(swarm.CheckExecutable(m, []string{"builder"})).To(MatchError(swarm.ErrNotExecutable))
	})

	It("marks a role that is not loaded as ErrNotExecutable", func() {
		Expect(swarm.CheckExecutable(m, []string{"lead"})).To(MatchError(swarm.ErrNotExecutable))
	})

	It("refuses a coordinator profile that is not loaded", func() {
		Expect(swarm.CheckExecutable(m, []string{"builder"})).To(MatchError(ContainSubstring(`coordinator profile "lead"`)))
	})

	It("refuses a role naming an unknown profile, with the packet id", func() {
		Expect(swarm.CheckExecutable(m, []string{"lead"})).To(MatchError(ContainSubstring(`packet a: role profile "builder"`)))
	})

	It("does not require a role on a packet that has none", func() {
		m.Packets = m.Packets[1:]
		Expect(swarm.CheckExecutable(m, []string{"lead"})).To(Succeed())
	})
})
