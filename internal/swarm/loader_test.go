package swarm_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("manifest loading of coordinator data", func() {
	doc := func(extra string) []byte {
		sha := bodyHashOf("build it")
		return []byte("name: demo\nscope: m2\n" + extra + "packets:\n  - id: a\n    scope: m2\n    task: build it\n" +
			"    source_sha256: " + sha + "\n" + "    role: builder\n" +
			"gates:\n  - name: tests\n    description: pass\n    command: \"true\"\n")
	}

	It("reads the coordinator", func() {
		m, err := swarm.ParseManifest(doc("coordinator: lead\n"))
		if err != nil {
			panic(err)
		}
		Expect(m.Coordinator).To(Equal("lead"))
	})

	It("reads a packet role", func() {
		m, err := swarm.ParseManifest(doc(""))
		if err != nil {
			panic(err)
		}
		Expect(m.Packets[0].Role).To(Equal("builder"))
	})

	It("leaves the coordinator empty when the manifest names none", func() {
		m, err := swarm.ParseManifest(doc(""))
		if err != nil {
			panic(err)
		}
		Expect(m.Coordinator).To(BeEmpty())
	})

	It("refuses an invalid coordinator name at load time", func() {
		_, err := swarm.ParseManifest(doc("coordinator: Not A Name\n"))
		Expect(err).To(MatchError(ContainSubstring("coordinator")))
	})

	It("keeps the canonical digest input of a manifest without the new fields unchanged", func() {
		m := swarm.Manifest{Name: "d", Scope: "s", Packets: []swarm.Packet{{ID: "a"}}}
		raw, err := json.Marshal(m)
		if err != nil {
			panic(err)
		}
		Expect(string(raw)).To(Equal(`{"Name":"d","Scope":"s","Packets":[{"ID":"a","Scope":"","Task":"",` +
			`"SourceSHA256":"","ParentID":"","Prerequisites":null}],"Gates":null}`))
	})
})
