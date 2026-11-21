package anthropic_test

import (
	"context"
	"errors"
	"strings"
	"unicode/utf16"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
)

func msgs(pairs ...string) *provider.ChatRequest {
	req := &provider.ChatRequest{}
	for i := 0; i < len(pairs); i += 2 {
		req.Messages = append(req.Messages, provider.Message{Role: pairs[i], Content: pairs[i+1]})
	}
	return req
}

// windowsUnits is the length in UTF-16 units of the Windows command line for
// argv. It is an upper bound: every argument is quoted, a quote is escaped
// with a backslash and the backslashes before it are doubled.
func windowsUnits(argv []string) int {
	var b strings.Builder
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		slashes := 0
		for _, c := range a {
			switch c {
			case '\\':
				slashes++
				continue
			case '"':
				b.WriteString(strings.Repeat(`\`, 2*slashes+1))
			default:
				b.WriteString(strings.Repeat(`\`, slashes))
			}
			slashes = 0
			b.WriteRune(c)
		}
		b.WriteString(strings.Repeat(`\`, 2*slashes) + `"`)
	}
	return len(utf16.Encode([]rune(b.String())))
}

var _ = Describe("Subscription request mapping", func() {
	var (
		fake *fakeRun
		p    *anthropic.Subscription
	)

	BeforeEach(func() {
		fake = &fakeRun{stdout: okEnd + "\n"}
		p = anthropic.NewSubscription("claude-test", fake.launcher())
	})

	run := func(req *provider.ChatRequest) anthropic.Command {
		drain(p.Stream(context.Background(), req))
		if len(fake.cmds) != 1 {
			panic("expected exactly one launch")
		}
		return fake.cmds[0]
	}
	withModel := func(model string) *provider.ChatRequest {
		req := userReq("hi")
		req.Model = model
		return req
	}

	It("keeps the worst-case system prompt within the Windows command line", func() {
		worst := msgs("system", strings.Repeat(`"`, anthropic.MaxSystemPromptBytes), "user", "hi")
		cmd := run(worst)
		argv := append([]string{strings.Repeat("b", 260)}, cmd.Args...) // a maximal binary path
		Expect(windowsUnits(argv)).To(BeNumerically("<=", 32767))
	})

	It("builds the headless argv with the default model", func() {
		Expect(run(userReq("hi")).Args).To(Equal([]string{
			"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
			"--no-session-persistence", "--tools", "", "--strict-mcp-config", "--setting-sources", "",
			"--model", "sonnet", "--system-prompt=",
		}))
	})

	DescribeTable("passes the requested model through unchanged",
		func(model string) {
			Expect(run(withModel(model)).Args).To(ContainElements("--model", model))
		},
		Entry("alias", "haiku"),
		Entry("full id", "claude-haiku-4-5-20251001"),
		Entry("bracketed alias", "sonnet[1m]"),
	)

	It("runs the configured binary", func() { Expect(run(userReq("hi")).Bin).To(Equal("claude-test")) })

	DescribeTable("always sends the labelled transcript on stdin, so it never starts with a slash",
		func(req *provider.ChatRequest, want string) {
			Expect(run(req).Stdin).To(Equal(want))
		},
		Entry("one message", userReq("secret prompt"), "User:\nsecret prompt"),
		Entry("a slash command prompt", userReq("/usage"), "User:\n/usage"),
		Entry("a multi-line prompt starting with a slash", userReq("/usage\nsecond line"),
			"User:\n/usage\nsecond line"),
		Entry("a multi-turn history", msgs("user", "one", "assistant", "two", "user", "three"),
			"User:\none\n\nAssistant:\ntwo\n\nUser:\nthree"),
	)

	It("adds --system-prompt joining system messages with blank lines", func() {
		Expect(run(msgs("system", "A", "system", "B", "user", "hi")).Args).To(ContainElement("--system-prompt=A\n\nB"))
	})
	It("binds a dash-leading system prompt to its flag", func() {
		Expect(run(msgs("system", "-be terse", "user", "hi")).Args).To(ContainElement("--system-prompt=-be terse"))
	})
	It("accepts a system prompt of exactly the limit", func() {
		sys := strings.Repeat("s", anthropic.MaxSystemPromptBytes)
		Expect(run(msgs("system", sys, "user", "hi")).Args).To(ContainElement("--system-prompt=" + sys))
	})
	It("replaces Claude Code's default system prompt with an empty one without system messages", func() {
		Expect(run(userReq("hi")).Args).To(ContainElement("--system-prompt="))
	})
	It("ignores MaxTokens", func() {
		req := userReq("hi")
		req.MaxTokens = 5
		Expect(run(req).Args).NotTo(ContainElement("5"))
	})

	DescribeTable("refuses before any launch, with a typed error",
		func(req *provider.ChatRequest, target error) {
			fake.launch = errors.New("launched")
			_, err := p.Chat(context.Background(), req)
			Expect(err).To(MatchError(target))
		},
		Entry("a nil request", (*provider.ChatRequest)(nil), anthropic.ErrInvalidRequest),
		Entry("no messages", &provider.ChatRequest{}, anthropic.ErrInvalidRequest),
		Entry("an empty prompt", userReq(""), anthropic.ErrInvalidRequest),
		Entry("a whitespace-only prompt", userReq(" \n\t"), anthropic.ErrInvalidRequest),
		Entry("an unsupported role", msgs("user", "hi", "function", "x"), anthropic.ErrInvalidRequest),
		Entry("a system prompt over the limit",
			msgs("system", strings.Repeat("s", anthropic.MaxSystemPromptBytes+1), "user", "hi"),
			anthropic.ErrSystemPromptTooLarge),
		Entry("a model with a leading dash", &provider.ChatRequest{Model: "--bare", Messages: userReq("hi").Messages},
			anthropic.ErrInvalidModel),
		Entry("a model with a space", &provider.ChatRequest{Model: "sonnet --bare", Messages: userReq("hi").Messages},
			anthropic.ErrInvalidModel),
		Entry("a model with a shell metacharacter",
			&provider.ChatRequest{Model: "sonnet;id", Messages: userReq("hi").Messages}, anthropic.ErrInvalidModel),
		Entry("tool definitions", &provider.ChatRequest{
			Tools: []provider.ToolDefinition{{Name: "read"}}, Messages: userReq("hi").Messages,
		}, anthropic.ErrUnsupportedTools),
		Entry("assistant tool calls", &provider.ChatRequest{Messages: []provider.Message{
			{Role: "user", Content: "hi"}, {Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "1", Name: "read"}}},
		}}, anthropic.ErrUnsupportedTools),
		Entry("the tool role", msgs("user", "hi", "tool", "out"), anthropic.ErrUnsupportedTools),
		Entry("a tool call id", &provider.ChatRequest{Messages: []provider.Message{
			{Role: "user", Content: "hi", ToolCallID: "1"},
		}}, anthropic.ErrUnsupportedTools),
	)
})
