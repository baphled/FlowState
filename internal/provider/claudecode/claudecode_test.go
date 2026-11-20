package claudecode_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/claudecode"
)

// fakeProc is a Process backed by canned output.
type fakeProc struct {
	out     io.Reader
	stderr  string
	waitErr error
	waited  *atomic.Bool
}

func (f *fakeProc) Stdout() io.Reader { return f.out }
func (f *fakeProc) Stderr() string    { return f.stderr }
func (f *fakeProc) Wait() error {
	if f.waited != nil {
		f.waited.Store(true)
	}
	return f.waitErr
}

// fakeRun records launches and serves canned output.
type fakeRun struct {
	cmds    []claudecode.Command
	stdout  string
	stderr  string
	waitErr error
	launch  error
}

func (f *fakeRun) launcher() claudecode.Launcher {
	return func(_ context.Context, c claudecode.Command) (claudecode.Process, error) {
		f.cmds = append(f.cmds, c)
		if f.launch != nil {
			return nil, f.launch
		}
		return &fakeProc{out: strings.NewReader(f.stdout), stderr: f.stderr, waitErr: f.waitErr}, nil
	}
}

func drain(ch <-chan provider.StreamEvent) []provider.StreamEvent {
	var evs []provider.StreamEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

func userReq(content string) *provider.ChatRequest {
	return &provider.ChatRequest{Messages: []provider.Message{{Role: "user", Content: content}}}
}

var _ = Describe("Provider", func() {
	var (
		fake *fakeRun
		p    *claudecode.Provider
	)

	BeforeEach(func() {
		fake = &fakeRun{stdout: `{"type":"result","is_error":false,"stop_reason":"end_turn"}` + "\n"}
		p = claudecode.New("claude-test", claudecode.WithLauncher(fake.launcher()))
	})

	run := func(req *provider.ChatRequest) claudecode.Command {
		drain(p.Stream(context.Background(), req))
		if len(fake.cmds) != 1 {
			panic("expected exactly one launch")
		}
		return fake.cmds[0]
	}

	It("is a streamer", func() {
		var _ provider.Streamer = p
		Expect(p.Name()).To(Equal("claude-code"))
	})

	It("lists the Claude Code aliases", func() {
		Expect(p.Models()).To(Equal([]string{"sonnet", "opus", "haiku"}))
	})

	It("builds the headless argv with the default model", func() {
		Expect(run(userReq("hi")).Args).To(Equal([]string{
			"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
			"--no-session-persistence", "--tools", "", "--strict-mcp-config", "--setting-sources", "",
			"--model", "sonnet",
		}))
	})

	It("passes a requested alias through unchanged", func() {
		req := userReq("hi")
		req.Model = "haiku"
		Expect(run(req).Args).To(ContainElements("--model", "haiku"))
	})

	It("passes a full model id through unchanged", func() {
		req := userReq("hi")
		req.Model = "claude-haiku-4-5-20251001"
		Expect(run(req).Args).To(ContainElement("claude-haiku-4-5-20251001"))
	})

	It("never passes --bare", func() {
		Expect(run(userReq("hi")).Args).NotTo(ContainElement("--bare"))
	})

	It("runs the configured binary", func() {
		Expect(run(userReq("hi")).Bin).To(Equal("claude-test"))
	})

	It("writes the prompt to stdin, not argv", func() {
		Expect(run(userReq("secret prompt")).Stdin).To(Equal("secret prompt"))
	})

	It("keeps the prompt out of argv", func() {
		Expect(run(userReq("secret prompt")).Args).NotTo(ContainElement(ContainSubstring("secret prompt")))
	})

	It("runs in the neutral temp directory", func() {
		Expect(run(userReq("hi")).Dir).To(Equal(os.TempDir()))
	})

	It("adds --system-prompt joining system messages with blank lines", func() {
		req := &provider.ChatRequest{Messages: []provider.Message{
			{Role: "system", Content: "A"}, {Role: "system", Content: "B"}, {Role: "user", Content: "hi"},
		}}
		Expect(run(req).Args).To(ContainElements("--system-prompt", "A\n\nB"))
	})

	It("omits --system-prompt without system messages", func() {
		Expect(run(userReq("hi")).Args).NotTo(ContainElement("--system-prompt"))
	})

	It("renders multi-turn history as a plain transcript", func() {
		req := &provider.ChatRequest{Messages: []provider.Message{
			{Role: "user", Content: "one"}, {Role: "assistant", Content: "two"}, {Role: "user", Content: "three"},
		}}
		Expect(run(req).Stdin).To(Equal("User:\none\n\nAssistant:\ntwo\n\nUser:\nthree"))
	})

	It("ignores MaxTokens", func() {
		req := userReq("hi")
		req.MaxTokens = 5
		Expect(run(req).Args).NotTo(ContainElement("5"))
	})

	It("rejects a request with no user message", func() {
		evs := drain(p.Stream(context.Background(), &provider.ChatRequest{}))
		Expect(evs).To(HaveLen(1))
	})

	DescribeTable("fails closed on tool surfaces without starting a process",
		func(req *provider.ChatRequest) {
			evs := drain(p.Stream(context.Background(), req))
			if len(evs) != 1 {
				panic("expected one event")
			}
			Expect(errors.Is(evs[0].Err, claudecode.ErrUnsupportedTools)).To(BeTrue())
		},
		Entry("tool definitions", &provider.ChatRequest{
			Tools:    []provider.ToolDefinition{{Name: "read"}},
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		}),
		Entry("assistant tool calls", &provider.ChatRequest{Messages: []provider.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "1", Name: "read"}}},
		}}),
		Entry("tool role", &provider.ChatRequest{Messages: []provider.Message{
			{Role: "user", Content: "hi"}, {Role: "tool", Content: "out"},
		}}),
		Entry("tool call id", &provider.ChatRequest{Messages: []provider.Message{
			{Role: "user", Content: "hi", ToolCallID: "1"},
		}}),
	)

	It("starts no process for a tool request", func() {
		drain(p.Stream(context.Background(), &provider.ChatRequest{
			Tools:    []provider.ToolDefinition{{Name: "read"}},
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		}))
		Expect(fake.cmds).To(BeEmpty())
	})

	It("collects the stream in Chat", func() {
		fake.stdout = `{"type":"stream_event","event":{"type":"content_block_delta",` +
			`"delta":{"type":"text_delta","text":"Hel"}}}` + "\n" +
			`{"type":"stream_event","event":{"type":"content_block_delta",` +
			`"delta":{"type":"text_delta","text":"lo"}}}` + "\n" +
			`{"type":"result","is_error":false,"stop_reason":"end_turn"}` + "\n"
		resp, err := p.Chat(context.Background(), userReq("hi"))
		if err != nil {
			panic(err)
		}
		Expect(resp.Content).To(Equal("Hello"))
	})

	It("returns the provider error from Chat", func() {
		fake.stdout = `{"type":"result","is_error":true,"result":"boom"}` + "\n"
		_, err := p.Chat(context.Background(), userReq("hi"))
		Expect(err).To(MatchError(ContainSubstring("boom")))
	})

	It("surfaces a launch failure as a terminal error", func() {
		fake.launch = errors.New("spawn failed")
		evs := drain(p.Stream(context.Background(), userReq("hi")))
		Expect(evs).To(ConsistOf(HaveField("Err", MatchError("spawn failed"))))
	})

	It("kills the process and closes the channel on cancellation", func() {
		pr, pw := io.Pipe()
		var waited atomic.Bool
		launch := func(ctx context.Context, _ claudecode.Command) (claudecode.Process, error) {
			go func() { <-ctx.Done(); _ = pw.Close() }()
			return &fakeProc{out: pr, waited: &waited}, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch := claudecode.New("x", claudecode.WithLauncher(launch)).Stream(ctx, userReq("hi"))
		cancel()
		Eventually(func() bool {
			select {
			case _, ok := <-ch:
				return !ok
			default:
				return false
			}
		}).WithTimeout(5 * time.Second).WithPolling(5 * time.Millisecond).Should(BeTrue())
	})

	It("reaps the process on cancellation", func() {
		pr, pw := io.Pipe()
		var waited atomic.Bool
		launch := func(ctx context.Context, _ claudecode.Command) (claudecode.Process, error) {
			go func() { <-ctx.Done(); _ = pw.Close() }()
			return &fakeProc{out: pr, waited: &waited}, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch := claudecode.New("x", claudecode.WithLauncher(launch)).Stream(ctx, userReq("hi"))
		cancel()
		Eventually(waited.Load).WithTimeout(5 * time.Second).WithPolling(5 * time.Millisecond).Should(BeTrue())
		drain(ch)
	})

	It("names the binary and the fix when it is missing", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "no-such-claude")
		_, err := claudecode.New(missing).Chat(context.Background(), userReq("hi"))
		Expect(err).To(MatchError(ContainSubstring(missing)))
	})

	It("marks a missing binary with ErrBinaryNotFound", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "no-such-claude")
		_, err := claudecode.New(missing).Chat(context.Background(), userReq("hi"))
		Expect(errors.Is(err, claudecode.ErrBinaryNotFound)).To(BeTrue())
	})
})
