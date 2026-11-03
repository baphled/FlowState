package app_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("SwarmRun", func() {
	var manifest swarm.Manifest

	BeforeEach(func() {
		manifest = swarm.Manifest{
			Name:  "demo.swarm",
			Scope: "demo.swarm",
			Packets: []swarm.Packet{{
				ID:           "p1",
				Scope:        "demo.swarm.plan",
				Task:         "plan the work",
				SourceSHA256: swarm.SHA256HexForTest([]byte("plan the work")),
			}},
			Gates: []swarm.Gate{{Name: "go", Description: "go", Command: "true"}},
		}
	})

	It("runs a manifest end to end with persistent records", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		res, runErr := app.SwarmRunManifest(context.Background(), dbPath, manifest)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Admitted).To(Equal([]string{"p1"}))
	})

	It("accepts only when every gate passes", func() {
		bad := manifest
		bad.Gates = []swarm.Gate{{Name: "broken", Description: "b", Command: "false"}}
		dbPath := filepath.Join(GinkgoT().TempDir(), "run.db")
		_, runErr := app.SwarmRunManifest(context.Background(), dbPath, bad)
		Expect(runErr).To(MatchError(ContainSubstring("gates failed")))
	})
})
