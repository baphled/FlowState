package recall_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	recall "github.com/baphled/flowstate/internal/recall"
)

type dummyEvent struct {
	ID   string
	Data string
}

var _ = Describe("DiscoveryStore", func() {
	var store recall.DiscoveryStore

	BeforeEach(func() {
		// Will be replaced with actual FileDiscoveryStore when implemented
		_ = os.Remove("/tmp/test.jsonl")
		var err error
		store, err = recall.NewFileDiscoveryStore("/tmp/test.jsonl")
		Expect(err).NotTo(HaveOccurred())
	})

	It("publishes and queries events", func() {
		event := &dummyEvent{ID: "1", Data: "foo"}
		err := store.Publish(event)
		Expect(err).NotTo(HaveOccurred())

		results, err := store.Query(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
	})
})
