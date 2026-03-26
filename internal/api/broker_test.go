package api_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("SessionBroker", func() {
	var broker *api.SessionBroker

	BeforeEach(func() {
		broker = api.NewSessionBroker()
	})

	Describe("Subscribe", func() {
		It("returns a receive channel and unsubscribe func", func() {
			ch, unsub := broker.Subscribe("sess-1")
			Expect(ch).NotTo(BeNil())
			Expect(unsub).NotTo(BeNil())
			unsub()
		})
	})

	Describe("Publish", func() {
		It("forwards chunks to a subscriber", func() {
			ch, unsub := broker.Subscribe("sess-1")
			defer unsub()

			source := make(chan provider.StreamChunk, 2)
			source <- provider.StreamChunk{Content: "hello"}
			source <- provider.StreamChunk{Done: true}
			close(source)

			done := make(chan struct{})
			go func() {
				broker.Publish("sess-1", source)
				close(done)
			}()

			var received []provider.StreamChunk
			for {
				select {
				case chunk, ok := <-ch:
					if !ok {
						goto collect
					}
					received = append(received, chunk)
				case <-time.After(2 * time.Second):
					Fail("timed out waiting for chunks")
				}
			}
		collect:
			<-done
			Expect(received).To(HaveLen(2))
			Expect(received[0].Content).To(Equal("hello"))
			Expect(received[1].Done).To(BeTrue())
		})

		It("closes subscriber channel when source closes", func() {
			ch, _ := broker.Subscribe("sess-2")

			source := make(chan provider.StreamChunk)
			close(source)

			go broker.Publish("sess-2", source)

			Eventually(ch, 2*time.Second).Should(BeClosed())
		})

		It("forwards chunks to multiple subscribers", func() {
			ch1, unsub1 := broker.Subscribe("sess-3")
			ch2, unsub2 := broker.Subscribe("sess-3")
			defer unsub1()
			defer unsub2()

			source := make(chan provider.StreamChunk, 1)
			source <- provider.StreamChunk{Content: "ping"}
			close(source)

			done := make(chan struct{})
			go func() {
				broker.Publish("sess-3", source)
				close(done)
			}()

			<-done

			Eventually(func() int {
				select {
				case <-ch1:
					return 1
				default:
					return 0
				}
			}, time.Second).Should(Equal(1))

			Eventually(func() int {
				select {
				case <-ch2:
					return 1
				default:
					return 0
				}
			}, time.Second).Should(Equal(1))
		})
	})

	Describe("Unsubscribe", func() {
		It("closes the subscriber channel on unsubscribe", func() {
			ch, unsub := broker.Subscribe("sess-4")
			unsub()
			Eventually(ch, time.Second).Should(BeClosed())
		})
	})
})
