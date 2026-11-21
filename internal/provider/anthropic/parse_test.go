package anthropic_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
)

const (
	deltaA = `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"A"}}}`
	deltaB = `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"B"}}}`
	asstAB = `{"type":"assistant","message":{"content":[{"type":"text","text":"AB"}]}}`
	okEnd  = `{"type":"result","is_error":false,"stop_reason":"end_turn"}`
	think  = `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"x"}}}`
)

var finish = provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "stop"}

func text(s string) provider.StreamEvent { return provider.StreamEvent{Content: s} }

var _ = Describe("stream parsing", func() {
	var fake *fakeRun

	stream := func(lines ...string) []provider.StreamEvent {
		fake.stdout = strings.Join(lines, "\n") + "\n"
		p := anthropic.NewSubscription("c", fake.launcher())
		return drain(p.Stream(context.Background(), userReq("hi")))
	}
	last := func(evs []provider.StreamEvent) provider.StreamEvent { return evs[len(evs)-1] }

	BeforeEach(func() { fake = &fakeRun{} })

	DescribeTable("maps lines to events",
		func(want []provider.StreamEvent, lines ...string) {
			Expect(stream(lines...)).To(Equal(want))
		},
		Entry("one content event per text delta, in order", []provider.StreamEvent{text("A"), text("B"), finish},
			deltaA, deltaB, okEnd),
		Entry("the assistant line when no delta was emitted", []provider.StreamEvent{text("AB"), finish},
			asstAB, okEnd),
		Entry("no duplicate of content already streamed", []provider.StreamEvent{text("A"), text("B"), finish},
			deltaA, deltaB, asstAB, okEnd),
		Entry("the delta rule applies per turn", []provider.StreamEvent{text("A"), text("AB"), finish},
			deltaA, asstAB, asstAB, okEnd),
		Entry("nothing for unknown types and non-JSON lines", []provider.StreamEvent{text("A"), finish},
			"not json", `{"type":"mystery"}`, `{"type":"system","subtype":"init"}`, deltaA, okEnd),
		Entry("nothing for non-text deltas", []provider.StreamEvent{finish}, think, okEnd),
		Entry("a stop finish for a null stop_reason", []provider.StreamEvent{finish},
			`{"type":"result","is_error":false,"stop_reason":null}`),
		Entry("a stop finish for a missing stop_reason", []provider.StreamEvent{finish},
			`{"type":"result","is_error":false}`),
		Entry("a finish for subtype success", []provider.StreamEvent{finish},
			`{"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn"}`),
	)

	DescribeTable("the finish reason follows the result stop_reason",
		func(result, want string) {
			Expect(last(stream(result)).FinishReason).To(Equal(want))
		},
		Entry("end_turn becomes stop", `{"type":"result","is_error":false,"stop_reason":"end_turn"}`, "stop"),
		Entry("stop_sequence becomes stop", `{"type":"result","is_error":false,"stop_reason":"stop_sequence"}`, "stop"),
		Entry("null becomes stop", `{"type":"result","is_error":false,"stop_reason":null}`, "stop"),
		Entry("absent becomes stop", `{"type":"result","is_error":false}`, "stop"),
		Entry("max_tokens becomes length", `{"type":"result","is_error":false,"stop_reason":"max_tokens"}`, "length"),
		Entry("refusal passes through", `{"type":"result","is_error":false,"stop_reason":"refusal"}`, "refusal"),
		Entry("pause_turn passes through", `{"type":"result","is_error":false,"stop_reason":"pause_turn"}`, "pause_turn"),
		Entry("tool_use passes through", `{"type":"result","is_error":false,"stop_reason":"tool_use"}`, "tool_use"),
	)

	DescribeTable("turns a failed result into a terminal error",
		func(matcher types.GomegaMatcher, lines ...string) {
			Expect(last(stream(lines...)).Err).To(matcher)
		},
		Entry("with its message", MatchError(ContainSubstring("overloaded")),
			`{"type":"result","is_error":true,"result":"overloaded","api_error_status":529}`),
		Entry("not signed in on a numeric 401", MatchError(anthropic.ErrNotSignedIn),
			`{"type":"result","is_error":true,"result":"nope","api_error_status":401}`),
		Entry("not signed in on a string 401", MatchError(anthropic.ErrNotSignedIn),
			`{"type":"result","is_error":true,"result":"nope","api_error_status":"401"}`),
		Entry("naming the /login fix", MatchError(ContainSubstring("run `claude` and use /login")),
			`{"type":"result","is_error":true,"result":"nope","api_error_status":401}`),
		Entry("keeping the line when api_error_status has an odd type", MatchError(ContainSubstring("boom")),
			`{"type":"result","is_error":true,"result":"boom","api_error_status":{"x":[1]}}`),
		Entry("not signed in when only the assistant line signals it", MatchError(anthropic.ErrNotSignedIn),
			`{"type":"assistant","error":"authentication_failed","message":{"content":[]}}`),
		Entry("for a non-success subtype even when is_error is false", MatchError(ContainSubstring("error_max_turns")),
			`{"type":"result","subtype":"error_max_turns","is_error":false}`),
		Entry("falling back to the subtype for a blank message", MatchError(ContainSubstring("error_during_execution")),
			`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"  "}`),
		Entry("falling back to fixed text for subtype success with a blank message", MatchError(MatchRegexp(`: unknown error$`)),
			`{"type":"result","subtype":"success","is_error":true,"result":""}`),
		Entry("falling back to fixed text with no message or subtype", MatchError(MatchRegexp(`: unknown error$`)),
			`{"type":"result","is_error":true}`),
	)

	DescribeTable("produces a stream the provider validator accepts",
		func(result string) {
			var v provider.StreamValidator
			rejected := func(ev provider.StreamEvent) bool { return v.Accept(ev) != nil }
			Expect(slices.IndexFunc(stream(deltaA, result), rejected)).To(Equal(-1))
		},
		Entry("with a named stop_reason", okEnd),
		Entry("with a missing stop_reason", `{"type":"result","is_error":false}`),
		Entry("with max_tokens, finishing length", `{"type":"result","is_error":false,"stop_reason":"max_tokens"}`),
		Entry("with a passthrough stop_reason", `{"type":"result","is_error":false,"stop_reason":"refusal"}`),
	)

	Describe("the recorded signed-out session (subtype success, is_error true)", func() {
		It("yields only the not-signed-in error and no content", func() {
			raw, err := os.ReadFile("testdata/unauthenticated.jsonl")
			if err != nil {
				panic(err)
			}
			Expect(stream(strings.TrimSpace(string(raw)))).To(ConsistOf(And(
				HaveField("Err", MatchError(anthropic.ErrNotSignedIn)), HaveField("Content", ""))))
		})
	})

	It("includes the stderr tail when the process exits non-zero without a result", func() {
		fake.stderr = strings.Repeat("x", 5000) + "boom-tail"
		fake.waitErr = errors.New("exit status 1")
		Expect(last(stream(deltaA)).Err).To(MatchError(ContainSubstring("boom-tail")))
	})

	It("bounds the stderr tail in the error", func() {
		fake.stderr = strings.Repeat("x", 5000)
		fake.waitErr = errors.New("exit status 1")
		Expect(len(last(stream(deltaA)).Err.Error())).To(BeNumerically("<", 2300))
	})

	It("errors when the stream ends cleanly without a result", func() {
		Expect(last(stream(deltaA)).Err).To(MatchError(ContainSubstring("without a result")))
	})

	It("reads a text delta longer than the reader's buffer", func() {
		long := strings.Repeat("y", 100000)
		line := strings.Replace(deltaA, `"A"`, `"`+long+`"`, 1)
		Expect(stream(line, okEnd)).To(Equal([]provider.StreamEvent{text(long), finish}))
	})
})
