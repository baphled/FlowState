package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/session"
)

// usageFinish is a finish event that carries u, or no usage when u is nil.
func usageFinish(u *provider.Usage) provider.StreamEvent {
	return provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "stop", Usage: u}
}

// usageLoop runs a loop whose turns are scripted with the given finish usages
// (nil: the turn did not report). Every turn but the last asks for a tool.
func usageLoop(usages ...*provider.Usage) (ExecLoopConfig, *ExecLoopResult) {
	turns := make([][]provider.StreamEvent, 0, len(usages))
	for i, u := range usages {
		events := []provider.StreamEvent{provText("turn")}
		if i < len(usages)-1 {
			events = append(events, provCall(echoCall("c"+string(rune('a'+i)))))
		}
		turns = append(turns, append(events, usageFinish(u)))
	}
	cfg := execLoopFixture(&execLoopStreamer{turns: turns})
	return cfg, runExec(context.Background(), cfg).Result
}

// messagePayloads are the payloads of the loop's assistant.message events.
func messagePayloads(cfg ExecLoopConfig) []map[string]any {
	out := []map[string]any{}
	for _, e := range eventsOf(cfg) {
		if e.Type == TypeAssistantMessage {
			out = append(out, payloadMap(e.PayloadJSON))
		}
	}
	return out
}

// messageSHAs are the payload hashes of the loop's assistant.message events.
func messageSHAs(cfg ExecLoopConfig) []string {
	out := []string{}
	for _, e := range eventsOf(cfg) {
		if e.Type == TypeAssistantMessage {
			out = append(out, e.PayloadSHA)
		}
	}
	return out
}

var (
	usageA = &provider.Usage{InputTokens: 10, OutputTokens: 2}
	usageC = &provider.Usage{InputTokens: 20, OutputTokens: 3, CacheReadTokens: 5, CacheWriteTokens: 7}
)

var _ = Describe("Provider usage in the execution loop", func() {
	It("persists the reported usage on the assistant.message event", func() {
		cfg, _ := usageLoop(usageC)
		Expect(messagePayloads(cfg)[0]["usage"]).To(Equal(map[string]any{
			"input_tokens": float64(20), "output_tokens": float64(3),
			"cache_read_tokens": float64(5), "cache_write_tokens": float64(7)}))
	})

	It("omits the usage field when the provider reported none", func() {
		cfg, _ := usageLoop(nil)
		Expect(messagePayloads(cfg)[0]).NotTo(HaveKey("usage"))
	})

	It("persists a reported zero as zeros, not as absent", func() {
		cfg, _ := usageLoop(&provider.Usage{})
		Expect(messagePayloads(cfg)[0]).To(HaveKey("usage"))
	})

	It("sums the turns that reported usage and counts them", func() {
		_, res := usageLoop(usageA, nil, usageC)
		Expect(res.Usage).To(Equal(UsageTotals{InputTokens: 30, OutputTokens: 5,
			CacheReadTokens: 5, CacheWriteTokens: 7, UsageTurns: 2}))
	})

	It("reports the context of the last turn that reported usage", func() {
		_, res := usageLoop(usageA, usageC, nil)
		Expect(res.LastContextTokens).To(Equal(int64(35)))
	})

	It("marks the last context reported when a turn reported usage", func() {
		_, res := usageLoop(usageA, nil)
		Expect(res.LastContextReported).To(BeTrue())
	})

	It("reports no last context when no turn reported usage", func() {
		_, res := usageLoop(nil, nil)
		Expect(res.LastContextReported).To(BeFalse())
	})

	It("leaves the totals at zero when no turn reported usage", func() {
		_, res := usageLoop(nil)
		Expect(res.Usage).To(Equal(UsageTotals{}))
	})

	It("replays the same history with and without usage", func() {
		with, _ := usageLoop(usageA, usageC)
		without, _ := usageLoop(nil, nil)
		Expect(replayedHistory(context.Background(), ExecLoopConfig{Store: with.Store, Session: with.Session})).
			To(Equal(replayedHistory(context.Background(), ExecLoopConfig{Store: without.Store, Session: without.Session})))
	})

	It("keeps the payload hash of an event written without usage", func() {
		cfg, _ := usageLoop(nil)
		Expect(messageSHAs(cfg)).To(Equal([]string{"5828c032a588d1db4fa91e0674be3b9ee7daed4c8af6d16047d77f9e93cf5ec8"}))
	})
})

var _ = Describe("Provider usage in the text turn", func() {
	turn := func(u *provider.Usage) ExecLoopConfig {
		cfg := execLoopFixture(&execLoopStreamer{turns: [][]provider.StreamEvent{{provText("hello"), usageFinish(u)}}})
		if _, err := Turn(context.Background(), cfg.Store, cfg.Session, cfg.Streamer); err != nil {
			panic(err)
		}
		return cfg
	}

	It("persists the reported usage on the assistant.message event", func() {
		Expect(messagePayloads(turn(usageA))[0]["usage"]).To(HaveKeyWithValue("input_tokens", float64(10)))
	})

	It("omits the usage field when the provider reported none", func() {
		Expect(messagePayloads(turn(nil))[0]).NotTo(HaveKey("usage"))
	})
})

var _ = Describe("Provider usage in the session doctor", func() {
	diagnose := func(cfg ExecLoopConfig) SessionUsage {
		d, err := DiagnoseSession(context.Background(), cfg.Store, cfg.Session.ID)
		if err != nil {
			panic(err)
		}
		return d.Usage
	}

	It("totals the usage the log records", func() {
		cfg, _ := usageLoop(usageA, nil, usageC)
		Expect(diagnose(cfg)).To(Equal(SessionUsage{TurnsTotal: 3, LastContextTokens: 35, Totals: UsageTotals{
			InputTokens: 30, OutputTokens: 5, CacheReadTokens: 5, CacheWriteTokens: 7, UsageTurns: 2}}))
	})

	It("reports an old log, which has no usage field, as not reported", func() {
		cfg := execLoopFixture(&execLoopStreamer{})
		w, err := cfg.Store.NewEventWriter(context.Background(), cfg.Session.ID)
		if err != nil {
			panic(err)
		}
		if _, err := w.Append(context.Background(), session.ActorEngine, TypeAssistantMessage,
			map[string]any{"content": "old"}); err != nil {
			panic(err)
		}
		if err := w.Close(); err != nil {
			panic(err)
		}
		Expect(diagnose(cfg)).To(Equal(SessionUsage{TurnsTotal: 1}))
	})

	It("replays an old log that has no usage field", func() {
		cfg := execLoopFixture(&execLoopStreamer{})
		w, err := cfg.Store.NewEventWriter(context.Background(), cfg.Session.ID)
		if err != nil {
			panic(err)
		}
		if _, err := w.Append(context.Background(), session.ActorEngine, TypeAssistantMessage,
			map[string]any{"content": "old"}); err != nil {
			panic(err)
		}
		if err := w.Close(); err != nil {
			panic(err)
		}
		Expect(replayedHistory(context.Background(), ExecLoopConfig{Store: cfg.Store, Session: cfg.Session})).
			To(Equal([]provider.Message{{Role: "user", Content: "do the thing"}}))
	})

	It("ignores a malformed usage field", func() {
		Expect(messageUsage(`{"content":"x","usage":"many"}`)).To(BeNil())
	})
})

// usageRoute runs a one-turn loop on streamer and returns the result.
func usageRoute(streamer provider.Streamer) *ExecLoopResult {
	cfg := execLoopFixture(&execLoopStreamer{})
	cfg.Streamer = streamer
	return runExec(context.Background(), cfg).Result
}

var _ = Describe("Provider usage by route", func() {
	It("reaches the loop from the Anthropic API-key route", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `data: {"type":"message_start","message":{"usage":{"input_tokens":12,`+
				`"cache_read_input_tokens":3,"cache_creation_input_tokens":2,"output_tokens":1}}}`+"\n\n"+
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`+"\n\n"+
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`+"\n\n"+
				`data: {"type":"message_stop"}`+"\n\n")
		}))
		DeferCleanup(ts.Close)
		res := usageRoute(anthropic.NewAPIKey("sk-ant-synthetic-test-key", ts.URL, nil))
		Expect(res.Usage).To(Equal(UsageTotals{InputTokens: 12, OutputTokens: 7,
			CacheReadTokens: 3, CacheWriteTokens: 2, UsageTurns: 1}))
	})

	It("does not reach the loop from the subscription route, even if the binary prints usage", func() {
		out := `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}}` + "\n" +
			`{"type":"result","subtype":"success","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":6}}` + "\n"
		launch := func(context.Context, anthropic.Command) (anthropic.Process, error) {
			return subscriptionProc{strings.NewReader(out)}, nil
		}
		res := usageRoute(anthropic.NewSubscription("claude-test", launch))
		Expect(res.Usage.UsageTurns).To(Equal(int64(0)))
	})
})

// subscriptionProc is a fake Claude Code process with canned stdout.
type subscriptionProc struct{ out io.Reader }

func (p subscriptionProc) Stdout() io.ReadCloser { return io.NopCloser(p.out) }
func (subscriptionProc) Stderr() string          { return "" }
func (subscriptionProc) Wait() error             { return nil }
