package claudecode_test

import (
	"context"
	"errors"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/claudecode"
)

const (
	deltaA = `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"A"}}}`
	deltaB = `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"B"}}}`
	asstAB = `{"type":"assistant","message":{"content":[{"type":"text","text":"AB"}]}}`
	okEnd  = `{"type":"result","is_error":false,"stop_reason":"end_turn"}`
)

var _ = Describe("stream parsing", func() {
	var fake *fakeRun

	stream := func(lines ...string) []provider.StreamEvent {
		fake.stdout = strings.Join(lines, "\n") + "\n"
		p := claudecode.New("c", claudecode.WithLauncher(fake.launcher()))
		return drain(p.Stream(context.Background(), userReq("hi")))
	}

	BeforeEach(func() { fake = &fakeRun{} })

	It("emits one content event per text delta", func() {
		Expect(stream(deltaA, deltaB, okEnd)).To(HaveLen(3))
	})

	It("emits delta text in order as plain content", func() {
		evs := stream(deltaA, deltaB, okEnd)
		Expect([]string{evs[0].Content, evs[1].Content}).To(Equal([]string{"A", "B"}))
	})

	It("uses the assistant line when no delta was emitted", func() {
		Expect(stream(asstAB, okEnd)[0].Content).To(Equal("AB"))
	})

	It("does not duplicate content already streamed as deltas", func() {
		Expect(stream(deltaA, deltaB, asstAB, okEnd)).To(HaveLen(3))
	})

	It("treats each turn separately for the delta rule", func() {
		Expect(stream(deltaA, asstAB, asstAB, okEnd)).To(HaveLen(3))
	})

	It("ends with a finish event carrying the stop reason", func() {
		evs := stream(deltaA, okEnd)
		Expect(evs[len(evs)-1]).To(Equal(provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "end_turn"}))
	})

	It("ignores unknown types and non-JSON lines", func() {
		Expect(stream("not json", `{"type":"mystery"}`, `{"type":"system","subtype":"init"}`, deltaA, okEnd)).To(HaveLen(2))
	})

	It("ignores non-text deltas", func() {
		thinking := `{"type":"stream_event","event":{"type":"content_block_delta",` +
			`"delta":{"type":"thinking_delta","thinking":"x"}}}`
		Expect(stream(thinking, okEnd)).To(HaveLen(1))
	})

	It("turns an error result into a terminal error with its message", func() {
		evs := stream(`{"type":"result","is_error":true,"result":"overloaded","api_error_status":529}`)
		Expect(evs).To(ConsistOf(HaveField("Err", MatchError(ContainSubstring("overloaded")))))
	})

	It("tells the user to sign in on a 401 result", func() {
		evs := stream(`{"type":"result","is_error":true,"result":"nope","api_error_status":401}`)
		Expect(errors.Is(evs[0].Err, claudecode.ErrNotSignedIn)).To(BeTrue())
	})

	It("names the /login fix on authentication failure", func() {
		evs := stream(`{"type":"result","is_error":true,"result":"nope","api_error_status":401}`)
		Expect(evs[0].Err).To(MatchError(ContainSubstring("run `claude` and use /login")))
	})

	Describe("the recorded signed-out session", func() {
		var evs []provider.StreamEvent

		BeforeEach(func() {
			raw, err := os.ReadFile("testdata/unauthenticated.jsonl")
			if err != nil {
				panic(err)
			}
			evs = stream(strings.TrimSpace(string(raw)))
		})

		It("yields exactly one terminal event", func() {
			Expect(evs).To(HaveLen(1))
		})

		It("yields the not-signed-in error", func() {
			Expect(errors.Is(evs[0].Err, claudecode.ErrNotSignedIn)).To(BeTrue())
		})

		It("never leaks the synthetic error text as content", func() {
			Expect(evs[0].Content).To(BeEmpty())
		})
	})

	It("reports not signed in when only the assistant line signals it", func() {
		evs := stream(`{"type":"assistant","error":"authentication_failed","message":{"content":[]}}`)
		Expect(errors.Is(evs[0].Err, claudecode.ErrNotSignedIn)).To(BeTrue())
	})

	It("includes a bounded stderr tail when the process exits non-zero without a result", func() {
		fake.stderr = strings.Repeat("x", 5000) + "boom-tail"
		fake.waitErr = errors.New("exit status 1")
		evs := stream(deltaA)
		Expect(evs[len(evs)-1].Err).To(MatchError(ContainSubstring("boom-tail")))
	})

	It("bounds the stderr tail in the error", func() {
		fake.stderr = strings.Repeat("x", 5000)
		fake.waitErr = errors.New("exit status 1")
		evs := stream(deltaA)
		Expect(len(evs[len(evs)-1].Err.Error())).To(BeNumerically("<", 2300))
	})

	It("errors when the stream ends cleanly without a result", func() {
		evs := stream(deltaA)
		Expect(evs[len(evs)-1].Err).To(MatchError(ContainSubstring("without a result")))
	})
})
