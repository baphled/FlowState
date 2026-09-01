package api_test

import (
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/api"
)

var _ = Describe("SSE thinking events", func() {
	It("writes a thinking fragment as a JSON server-sent event", func() {
		recorder := httptest.NewRecorder()
		consumer, ok := api.NewSSEConsumer(recorder)
		Expect(ok).To(BeTrue())

		consumer.WriteThinking("reasoning about the task")

		Expect(recorder.Body.String()).To(Equal("data: {\"type\":\"thinking\",\"content\":\"reasoning about the task\"}\n\n"),
			"thinking must travel as its own typed event so the frontend can route it to the thinking panel")
	})
})
