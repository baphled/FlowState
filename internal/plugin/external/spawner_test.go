package external_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/baphled/flowstate/internal/plugin/external"
)

var _ = Describe("Spawner and PluginProcess", func() {
	var (
		spawner *external.Spawner
		cancel  context.CancelFunc
	)

	BeforeEach(func() {
		_, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		spawner = &external.Spawner{}
		_ = spawner
	})

	AfterEach(func() {
		cancel()
	})

	It("spawns a real binary and communicates over stdio", func() {
		// TODO: Implement test for spawning /bin/cat or similar
	})

	It("returns error when binary does not exist", func() {
		// TODO: Implement test for nonexistent binary
	})

	It("kills the process when requested", func() {
		// TODO: Implement test for Kill
	})

	It("performs graceful shutdown, then forced kill if needed", func() {
		// TODO: Implement test for Shutdown
	})
})
