package swarm_test

import (
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
		res, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, nil)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Admitted).To(Equal([]string{"p1", "p2"}))
	})

	It("accepts the run when every gate passes", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		res, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, nil)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Gates.Accepted).To(BeTrue())
	})
	It("rejects acceptance even if gates pass when admission was rejected", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		m.Packets[0].SourceSHA256 = swarm.SHA256HexForTest([]byte("incorrect-source"))
		res, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, nil)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(res.Gates.Accepted).To(BeFalse())
	})

	It("records successful packet admissions before completing", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{}
		_, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(receipts.admissions).To(Equal([]bool{true, true}))
	})

	It("records packet rejections before gate evaluation", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		m.Packets[0].SourceSHA256 = swarm.SHA256HexForTest([]byte("incorrect-source"))
		receipts := &runReceipts{}
		_, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).NotTo(HaveOccurred())
		Expect(receipts.admissions).To(Equal([]bool{false, false}))
	})

	It("fails the run if admission evidence cannot be persisted", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{admissionErr: errors.New("durable admission unavailable")}
		_, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).To(MatchError(ContainSubstring("durable admission unavailable")))
	})

	It("fails the run if rejection evidence cannot be persisted", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		m.Packets[0].SourceSHA256 = swarm.SHA256HexForTest([]byte("incorrect-source"))
		receipts := &runReceipts{admissionErr: errors.New("durable rejection unavailable")}
		_, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).To(MatchError(ContainSubstring("durable rejection unavailable")))
	})

	It("propagates failed gate evidence persistence", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{gatesErr: errors.New("durable gates unavailable")}
		_, runErr := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(runErr).To(MatchError(ContainSubstring("durable gates unavailable")))
	})

	It("revokes gate acceptance after evidence persistence fails", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		receipts := &runReceipts{gatesErr: errors.New("durable gates unavailable")}
		res, _ := swarm.Run(m, hashFor(), swarm.NewGateRunner(), nil, receipts)
		Expect(res.Gates.Accepted).To(BeFalse())
	})

	It("rejects missing admission collaborators instead of panicking", func() {
		m, parseErr := swarm.ParseManifest([]byte(manifestYAML))
		Expect(parseErr).NotTo(HaveOccurred())
		_, runErr := swarm.Run(m, nil, swarm.NewGateRunner(), nil, nil)
		Expect(runErr).To(HaveOccurred())
	})

})

type runReceipts struct {
	admissionErr error
	gatesErr     error
	admissions   []bool
}

func (r *runReceipts) RecordAdmission(_ string, _ swarm.Packet, admitted bool, _ string) error {
	r.admissions = append(r.admissions, admitted)
	return r.admissionErr
}

func (r *runReceipts) RecordGates(_ string, _ swarm.GatesDecision) error {
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
