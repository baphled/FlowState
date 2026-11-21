package anthropic_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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

// promptSpy records the --system-prompt-file the way the child would find it
// at launch, then passes the command on to the wrapped launcher.
type promptSpy struct {
	path, text string
	mode       os.FileMode
}

func (s *promptSpy) wrap(next anthropic.Launcher) anthropic.Launcher {
	return func(ctx context.Context, c anthropic.Command) (anthropic.Process, error) {
		for i, a := range c.Args {
			if a != "--system-prompt-file" || i+1 >= len(c.Args) {
				continue
			}
			s.path = c.Args[i+1]
			b, _ := os.ReadFile(s.path)
			s.text = string(b)
			if fi, err := os.Stat(s.path); err == nil {
				s.mode = fi.Mode()
			}
		}
		return next(ctx, c)
	}
}

var _ = Describe("Subscription request mapping", func() {
	var (
		fake *fakeRun
		spy  *promptSpy
		p    *anthropic.Subscription
	)

	BeforeEach(func() {
		fake = &fakeRun{stdout: okEnd + "\n"}
		spy = &promptSpy{}
		p = anthropic.NewSubscription("claude-test", spy.wrap(fake.launcher()))
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

	Describe("a non-empty system prompt", func() {
		var cmd anthropic.Command

		BeforeEach(func() { cmd = run(msgs("system", "A", "system", "B", "user", "hi")) })

		It("is passed as --system-prompt-file with the path in the next argument", func() {
			Expect(cmd.Args).To(ContainElements("--system-prompt-file", spy.path))
		})
		It("sends no --system-prompt= argument", func() {
			Expect(cmd.Args).NotTo(ContainElement(HavePrefix("--system-prompt=")))
		})
		It("never appears in argv", func() { Expect(strings.Join(cmd.Args, " ")).NotTo(ContainSubstring("A\n\nB")) })
		It("is written to the file joining system messages with blank lines", func() {
			Expect(spy.text).To(Equal("A\n\nB"))
		})
		It("is written inside the request's working directory", func() {
			Expect(filepath.Join(cmd.Dir, anthropic.SystemPromptFile)).To(Equal(spy.path))
		})
		It("is written with mode 0600", func() {
			want := os.FileMode(0o600)
			if runtime.GOOS == "windows" { // modes are not enforced there; Go reports 0666
				want = 0o666
			}
			Expect(spy.mode.Perm()).To(Equal(want))
		})
		It("is removed with the working directory", func() {
			_, err := os.Stat(spy.path)
			Expect(err).To(MatchError(os.ErrNotExist))
		})
	})
	It("writes a dash-leading system prompt verbatim to the file", func() {
		run(msgs("system", "-be terse", "user", "hi"))
		Expect(spy.text).To(Equal("-be terse"))
	})
	It("accepts a system prompt of exactly the limit", func() {
		sys := strings.Repeat("s", anthropic.MaxSystemPromptBytes)
		run(msgs("system", sys, "user", "hi"))
		Expect(spy.text).To(Equal(sys))
	})
	It("raises the system prompt limit above the old argv bound of 12 KiB", func() {
		Expect(anthropic.MaxSystemPromptBytes).To(BeNumerically(">", 12<<10))
	})
	It("replaces Claude Code's default system prompt with an empty one without system messages", func() {
		Expect(run(userReq("hi")).Args).To(ContainElement("--system-prompt="))
	})
	It("writes no system prompt file without system messages", func() {
		run(userReq("hi"))
		Expect(spy.path).To(BeEmpty())
	})
	Describe("a caller-owned working directory", func() {
		var dir string

		BeforeEach(func() {
			dir = GinkgoT().TempDir()
			p = anthropic.NewSubscription("claude-test", spy.wrap(fake.launcher()),
				anthropic.WithWorkDir(func() (string, error) { return dir, nil }))
			run(msgs("system", "S", "user", "hi"))
		})

		It("received the prompt file inside it", func() { Expect(spy.path).To(Equal(filepath.Join(dir, anthropic.SystemPromptFile))) })
		It("is not removed", func() { Expect(dir).To(BeADirectory()) })
		It("is left without the prompt file", func() { Expect(spy.path).NotTo(BeAnExistingFile()) })
	})
	Describe("a file planted at the prompt path in a caller-owned directory", func() {
		var (
			target string
			err    error
		)

		BeforeEach(func() {
			dir := GinkgoT().TempDir()
			target = filepath.Join(GinkgoT().TempDir(), "victim")
			if werr := os.WriteFile(target, []byte("keep"), 0o600); werr != nil {
				panic(werr)
			}
			if lerr := os.Symlink(target, filepath.Join(dir, anthropic.SystemPromptFile)); lerr != nil {
				panic(lerr)
			}
			q := anthropic.NewSubscription("claude-test", spy.wrap(fake.launcher()),
				anthropic.WithWorkDir(func() (string, error) { return dir, nil }))
			_, err = q.Chat(context.Background(), msgs("system", "S", "user", "hi"))
		})

		It("is never followed: the request fails", func() { Expect(err).To(HaveOccurred()) })
		It("leaves the link's target untouched", func() {
			b, _ := os.ReadFile(target)
			Expect(string(b)).To(Equal("keep"))
		})
		It("starts no process", func() { Expect(fake.cmds).To(BeEmpty()) })
	})
	Describe("a regular file already at the prompt path in a caller-owned directory", func() {
		var (
			planted string
			err     error
		)

		BeforeEach(func() {
			dir := GinkgoT().TempDir()
			planted = filepath.Join(dir, anthropic.SystemPromptFile)
			if werr := os.WriteFile(planted, []byte("keep"), 0o600); werr != nil {
				panic(werr)
			}
			q := anthropic.NewSubscription("claude-test", spy.wrap(fake.launcher()),
				anthropic.WithWorkDir(func() (string, error) { return dir, nil }))
			_, err = q.Chat(context.Background(), msgs("system", "S", "user", "hi"))
		})

		It("is never overwritten: the request fails", func() { Expect(err).To(HaveOccurred()) })
		It("keeps its content", func() {
			b, _ := os.ReadFile(planted)
			Expect(string(b)).To(Equal("keep"))
		})
		It("starts no process", func() { Expect(fake.cmds).To(BeEmpty()) })
	})
	DescribeTable("hands the child an environment without the API-redirecting variable",
		func(name string) {
			GinkgoT().Setenv(name, "x")
			Expect(run(userReq("hi")).Env).NotTo(ContainElement(name + "=x"))
		},
		Entry("ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"),
		Entry("ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"),
		Entry("ANTHROPIC_BASE_URL", "ANTHROPIC_BASE_URL"),
		Entry("CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_BEDROCK"),
		Entry("CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_VERTEX"),
		Entry("CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_FOUNDRY"),
	)
	It("strips a redirecting variable whatever its case", func() {
		GinkgoT().Setenv("anthropic_api_key", "x")
		Expect(run(userReq("hi")).Env).NotTo(ContainElement(HavePrefix("anthropic_api_key=")))
	})
	It("keeps an anthropic variable that does not redirect authentication", func() {
		GinkgoT().Setenv("ANTHROPIC_MODEL", "kept")
		Expect(run(userReq("hi")).Env).To(ContainElement("ANTHROPIC_MODEL=kept"))
	})
	It("keeps an ordinary variable in the child's environment", func() {
		GinkgoT().Setenv("FLOWSTATE_SPEC_ORDINARY", "kept")
		Expect(run(userReq("hi")).Env).To(ContainElement("FLOWSTATE_SPEC_ORDINARY=kept"))
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
