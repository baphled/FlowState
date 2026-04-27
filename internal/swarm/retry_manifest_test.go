package swarm_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("Manifest retry + circuit_breaker schema", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "swarm-retry-manifest")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("parses the retry block with all fields", func() {
		path := filepath.Join(tempDir, "team.yml")
		body := `schema_version: "1.0.0"
id: team
lead: planner
members:
  - explorer
retry:
  max_attempts: 5
  initial_backoff: 100ms
  max_backoff: 30s
  multiplier: 2.5
  jitter: false
circuit_breaker:
  threshold: 7
  cooldown: 45s
  half_open_attempts: 2
`
		Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

		m, err := swarm.Load(path)

		Expect(err).NotTo(HaveOccurred())
		Expect(m.Retry).NotTo(BeNil())
		Expect(m.Retry.MaxAttempts).To(Equal(5))
		Expect(m.Retry.InitialBackoff).To(Equal(100 * time.Millisecond))
		Expect(m.Retry.MaxBackoff).To(Equal(30 * time.Second))
		Expect(m.Retry.Multiplier).To(BeNumerically("~", 2.5, 0.001))
		Expect(m.Retry.Jitter).To(BeFalse())
		Expect(m.CircuitBreaker).NotTo(BeNil())
		Expect(m.CircuitBreaker.Threshold).To(Equal(7))
		Expect(m.CircuitBreaker.Cooldown).To(Equal(45 * time.Second))
		Expect(m.CircuitBreaker.HalfOpenAttempts).To(Equal(2))
	})

	It("applies defaults when retry and circuit_breaker are absent", func() {
		path := filepath.Join(tempDir, "team.yml")
		body := `schema_version: "1.0.0"
id: team
lead: planner
members:
  - explorer
`
		Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

		m, err := swarm.Load(path)

		Expect(err).NotTo(HaveOccurred())
		eff := m.EffectiveRetryPolicy()
		Expect(eff.MaxAttempts).To(Equal(swarm.DefaultRetryMaxAttempts))
		Expect(eff.Multiplier).To(BeNumerically("~", swarm.DefaultRetryMultiplier, 0.001))
		Expect(eff.Jitter).To(BeTrue())
		Expect(eff.InitialBackoff).To(Equal(swarm.DefaultRetryInitialBackoff))
		Expect(eff.MaxBackoff).To(Equal(swarm.DefaultRetryMaxBackoff))

		breaker := m.EffectiveCircuitBreaker()
		Expect(breaker.Threshold).To(Equal(swarm.DefaultBreakerThreshold))
		Expect(breaker.Cooldown).To(Equal(swarm.DefaultBreakerCooldown))
		Expect(breaker.HalfOpenAttempts).To(Equal(swarm.DefaultBreakerHalfOpenAttempts))
	})

	It("rejects max_attempts below 1", func() {
		path := filepath.Join(tempDir, "team.yml")
		body := `schema_version: "1.0.0"
id: team
lead: planner
members:
  - explorer
retry:
  max_attempts: 0
`
		Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

		_, err := swarm.Load(path)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("retry.max_attempts"))
	})

	It("rejects circuit_breaker.threshold below 1", func() {
		path := filepath.Join(tempDir, "team.yml")
		body := `schema_version: "1.0.0"
id: team
lead: planner
members:
  - explorer
circuit_breaker:
  threshold: 0
`
		Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())

		_, err := swarm.Load(path)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("circuit_breaker.threshold"))
	})
})

var _ = Describe("Context sub_swarm_path", func() {
	It("seeds with the manifest chain_prefix", func() {
		m := &swarm.Manifest{
			SchemaVersion: swarm.SchemaVersionV1,
			ID:            "tech-team",
			Lead:          "planner",
			Members:       []string{"explorer"},
			Context:       swarm.ContextConfig{ChainPrefix: "tech"},
		}

		ctx := swarm.NewContext("tech-team", m)

		Expect(ctx.SubSwarmPath()).To(Equal("tech"))
	})

	It("falls back to the swarm id when chain_prefix is empty", func() {
		m := &swarm.Manifest{
			SchemaVersion: swarm.SchemaVersionV1,
			ID:            "team",
			Lead:          "planner",
		}

		ctx := swarm.NewContext("team", m)

		Expect(ctx.SubSwarmPath()).To(Equal("team"))
	})

	It("nests a child path under the parent path", func() {
		parent := swarm.Context{ChainPrefix: "bug-hunt"}

		child := parent.NestSubSwarm("cluster-2")

		Expect(child.SubSwarmPath()).To(Equal("bug-hunt/cluster-2"))
	})

	It("nests recursively to multi-level paths", func() {
		root := swarm.Context{ChainPrefix: "bug-hunt"}

		mid := root.NestSubSwarm("cluster-2")
		leaf := mid.NestSubSwarm("explorer-pod")

		Expect(leaf.SubSwarmPath()).To(Equal("bug-hunt/cluster-2/explorer-pod"))
	})
})
