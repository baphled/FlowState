package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// capturingStreamer records the ChatRequest each Stream call
// receives so model-override behaviour is observable.
type capturingStreamer struct {
	fakeStreamer
	got *provider.ChatRequest
}

func (c *capturingStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	c.got = req
	return c.fakeStreamer.Stream(ctx, req)
}

var _ = Describe("TurnWithOptions model override", func() {
	It("streams with the override model when ModelID is set", func() {
		st := newStore()
		ctx := context.Background()
		sess, err := session.Create(ctx, st, "fake", "fake-model", session.DefaultHarness, "sha", "say hi")
		Expect(err).NotTo(HaveOccurred())
		fake := &capturingStreamer{fakeStreamer: fakeStreamer{events: []provider.StreamEvent{
			{Content: "hi"},
		}}}
		_, err = TurnWithOptions(ctx, st, sess, fake, TurnOptions{ModelID: "glm-4.5-flash"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.got.Model).To(Equal("glm-4.5-flash"))
	})
})
