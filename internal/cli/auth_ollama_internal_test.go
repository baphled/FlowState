package cli

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ollamaProbe override", func() {
	var original func(string) error

	BeforeEach(func() {
		original = ollamaProbe
	})

	AfterEach(func() {
		ollamaProbe = original
	})

	It("can be overridden by tests", func() {
		called := ""
		ollamaProbe = func(host string) error {
			called = host
			return nil
		}

		err := ollamaProbe("http://example.test:11434")

		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(Equal("http://example.test:11434"))
	})

	It("propagates probe errors", func() {
		ollamaProbe = func(string) error { return errors.New("dial fail") }

		err := ollamaProbe("http://example.test:11434")

		Expect(err).To(MatchError(ContainSubstring("dial fail")))
	})
})
