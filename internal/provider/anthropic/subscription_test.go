package anthropic_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
)

const (
	within = 5 * time.Second
	poll   = 5 * time.Millisecond
)

// fakeProc is a Process backed by canned output; wait, when set, replaces Wait.
type fakeProc struct {
	out    io.ReadCloser
	stderr string
	wait   func() error
}

func (f *fakeProc) Stdout() io.ReadCloser { return f.out }
func (f *fakeProc) Stderr() string        { return f.stderr }
func (f *fakeProc) Wait() error {
	if f.wait != nil {
		return f.wait()
	}
	return nil
}

// fakeRun records launches and serves canned output.
type fakeRun struct {
	cmds       []anthropic.Command
	dirExisted bool
	stdout     string
	stderr     string
	waitErr    error
	launch     error
	proc       func(ctx context.Context) *fakeProc // when set, builds the process instead
}

func (f *fakeRun) launcher() anthropic.Launcher {
	return func(ctx context.Context, c anthropic.Command) (anthropic.Process, error) {
		f.cmds = append(f.cmds, c)
		if _, err := os.Stat(c.Dir); err == nil {
			f.dirExisted = true
		}
		if f.launch != nil {
			return nil, f.launch
		}
		if f.proc != nil {
			return f.proc(ctx), nil
		}
		return &fakeProc{out: io.NopCloser(strings.NewReader(f.stdout)), stderr: f.stderr,
			wait: func() error { return f.waitErr }}, nil
	}
}

func drain(ch <-chan provider.StreamEvent) []provider.StreamEvent {
	var evs []provider.StreamEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

// probeReader signals its first Read, so a spec knows the provider is reading.
type probeReader struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (r *probeReader) Read(b []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(b)
}

func userReq(content string) *provider.ChatRequest {
	return &provider.ChatRequest{Messages: []provider.Message{{Role: "user", Content: content}}}
}

var _ provider.Streamer = (*anthropic.Subscription)(nil)

var _ = Describe("Subscription", func() {
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
	It("is named anthropic", func() { Expect(p.Name()).To(Equal("anthropic")) })
	It("lists the Claude Code aliases", func() { Expect(p.Models()).To(Equal([]string{"sonnet", "opus", "haiku"})) })

	Describe("the working directory", func() {
		var cmd anthropic.Command

		BeforeEach(func() { cmd = run(userReq("hi")) })

		It("is not the shared temp directory", func() { Expect(cmd.Dir).NotTo(Equal(os.TempDir())) })
		It("is a fresh directory that exists when the child starts", func() { Expect(fake.dirExisted).To(BeTrue()) })

		It("is already removed when the finish event is delivered", func() {
			ch := p.Stream(context.Background(), userReq("hi"))
			<-ch
			DeferCleanup(func() { drain(ch) })
			_, err := os.Stat(fake.cmds[len(fake.cmds)-1].Dir)
			Expect(err).To(MatchError(os.ErrNotExist))
		})
	})
	It("removes the default working directory when the launch fails", func() {
		fake.launch = errors.New("spawn failed")
		drain(p.Stream(context.Background(), userReq("hi")))
		Expect(fake.cmds[0].Dir).NotTo(BeADirectory())
	})

	Describe("a directory supplied by a factory", func() {
		var marker string

		BeforeEach(func() {
			// The shape the default removal would accept: prefixed, directly in the temp dir.
			dir, err := os.MkdirTemp("", anthropic.WorkDirPrefix)
			if err != nil {
				panic(err)
			}
			DeferCleanup(os.RemoveAll, dir)
			marker = filepath.Join(dir, "marker")
			if err = os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
				panic(err)
			}
			p = anthropic.NewSubscription("claude-test", fake.launcher(),
				anthropic.WithWorkDir(func() (string, error) { return dir, nil }))
			drain(p.Stream(context.Background(), userReq("hi")))
		})

		It("is never removed", func() { Expect(marker).To(BeAnExistingFile()) })
	})

	DescribeTable("a factory result that is unusable",
		func(dir string) {
			p = anthropic.NewSubscription("claude-test", fake.launcher(),
				anthropic.WithWorkDir(func() (string, error) { return dir, nil }))
			_, err := p.Chat(context.Background(), userReq("hi"))
			Expect(err).To(MatchError(anthropic.ErrInvalidWorkDir))
		},
		Entry("is empty", ""),
		Entry("is relative", "relative/dir"),
		Entry("is not clean", os.TempDir()+string(filepath.Separator)+".."),
	)

	It("keeps the default factory when WithWorkDir is given nil", func() {
		p = anthropic.NewSubscription("claude-test", fake.launcher(), anthropic.WithWorkDir(nil))
		drain(p.Stream(context.Background(), userReq("hi")))
		Expect(fake.cmds).To(HaveLen(1))
	})

	DescribeTable("creates no working directory for a refused request",
		func(req *provider.ChatRequest) {
			var made atomic.Int32
			p = anthropic.NewSubscription("claude-test", fake.launcher(),
				anthropic.WithWorkDir(func() (string, error) { made.Add(1); return os.TempDir(), nil }))
			drain(p.Stream(context.Background(), req))
			Expect(made.Load()).To(BeZero())
		},
		Entry("a tool request", &provider.ChatRequest{
			Tools: []provider.ToolDefinition{{Name: "read"}}, Messages: userReq("hi").Messages}),
		Entry("an invalid model", &provider.ChatRequest{Model: "--bare", Messages: userReq("hi").Messages}),
		Entry("a blank prompt", userReq(" ")),
	)

	It("refuses a nil launcher with a terminal error", func() {
		_, err := anthropic.NewSubscription("c", nil).Chat(context.Background(), userReq("hi"))
		Expect(err).To(MatchError(anthropic.ErrNoLauncher))
	})
	It("ends a process with no stdout in a terminal error", func() {
		fake.proc = func(context.Context) *fakeProc { return &fakeProc{} }
		_, err := p.Chat(context.Background(), userReq("hi"))
		Expect(err).To(MatchError(ContainSubstring("without a result")))
	})
	It("collects the stream in Chat", func() {
		fake.stdout = strings.Join([]string{deltaA, deltaB, okEnd}, "\n") + "\n"
		resp, err := p.Chat(context.Background(), userReq("hi"))
		if err != nil {
			panic(err)
		}
		Expect(resp.Content).To(Equal("AB"))
	})
	It("surfaces a launch failure as a terminal error", func() {
		fake.launch = errors.New("spawn failed")
		Expect(drain(p.Stream(context.Background(), userReq("hi")))).To(ConsistOf(HaveField("Err", MatchError("spawn failed"))))
	})

	DescribeTable("a result line against a 200-byte cap",
		func(size int, want types.GomegaMatcher) {
			fake.stdout = `{"type":"result","pad":"` + strings.Repeat("x", size-len(`{"type":"result","pad":""}`)) + `"}` + "\n"
			_, err := anthropic.NewSubscription("c", fake.launcher(), anthropic.WithMaxLine(200)).Chat(context.Background(), userReq("hi"))
			Expect(err).To(want)
		},
		Entry("of exactly the cap passes", 200, Succeed()),
		Entry("one byte over the cap fails", 201, MatchError(ContainSubstring("exceeds the 200-byte limit"))),
	)

	// lingering serves out and then keeps the child alive until it is killed.
	lingering := func(out string) anthropic.Launcher {
		return func(ctx context.Context, _ anthropic.Command) (anthropic.Process, error) {
			return &fakeProc{out: io.NopCloser(strings.NewReader(out)), wait: func() error { <-ctx.Done(); return nil }}, nil
		}
	}
	chatsWithin := func(q *anthropic.Subscription) <-chan error {
		done := make(chan error, 1)
		go func() { _, err := q.Chat(context.Background(), userReq("hi")); done <- err }()
		return done
	}

	It("kills the child when the line cap trips", func() {
		q := anthropic.NewSubscription("c", lingering(strings.Repeat("x", 200)+"\n"),
			anthropic.WithMaxLine(64), anthropic.WithResultGrace(time.Hour))
		Eventually(chatsWithin(q)).WithTimeout(within).WithPolling(poll).Should(Receive(MatchError(anthropic.ErrLineTooLong)))
	})
	It("kills a child that lingers after its result line once the grace period passes", func() {
		q := anthropic.NewSubscription("c", lingering(okEnd+"\n"), anthropic.WithResultGrace(20*time.Millisecond))
		Eventually(chatsWithin(q)).WithTimeout(within).WithPolling(poll).Should(Receive(BeNil()))
	})
	It("documents a five second default grace period", func() {
		Expect(anthropic.DefaultResultGrace).To(Equal(5 * time.Second))
	})

	// These drive the provider with fake pipes; the real process arrives with the launcher packet.
	Describe("cancelling while the stdout never ends", func() {
		var (
			ch     <-chan provider.StreamEvent
			waited atomic.Bool
		)

		BeforeEach(func() {
			pr, pw := io.Pipe()
			DeferCleanup(pw.Close)
			waited.Store(false)
			probe := &probeReader{ReadCloser: pr, started: make(chan struct{})}
			fake.proc = func(ctx context.Context) *fakeProc {
				return &fakeProc{out: probe, wait: func() error { <-ctx.Done(); waited.Store(true); return nil }}
			}
			ctx, cancel := context.WithCancel(context.Background())
			ch = p.Stream(ctx, userReq("hi"))
			select {
			case <-probe.started: // the provider is now reading the silent pipe
			case <-time.After(within):
				panic("provider never read the pipe")
			}
			cancel()
		})

		It("closes the channel", func() {
			Eventually(ch).WithTimeout(within).WithPolling(poll).Should(BeClosed())
		})
		It("waits on the process", func() {
			Eventually(waited.Load).WithTimeout(within).WithPolling(poll).Should(BeTrue())
		})
		It("removes the working directory", func() {
			Eventually(fake.cmds[0].Dir).WithTimeout(within).WithPolling(poll).ShouldNot(BeADirectory())
		})
	})

	Describe("a context that is already cancelled", func() {
		var ctx context.Context

		BeforeEach(func() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		})

		It("launches no process", func() {
			drain(p.Stream(ctx, userReq("hi")))
			Expect(fake.cmds).To(BeEmpty())
		})
		It("makes Chat return context.Canceled", func() {
			_, err := p.Chat(ctx, userReq("hi"))
			Expect(err).To(MatchError(context.Canceled))
		})
	})

	It("returns context.Canceled, not partial content, when Chat is cancelled mid-stream", func() {
		pr, pw := io.Pipe()
		DeferCleanup(pw.Close)
		probe := &probeReader{ReadCloser: pr, started: make(chan struct{})}
		fake.proc = func(ctx context.Context) *fakeProc {
			return &fakeProc{out: probe, wait: func() error { <-ctx.Done(); return nil }}
		}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() { _, err := p.Chat(ctx, userReq("hi")); done <- err }()
		go func() { _, _ = pw.Write([]byte(deltaA + "\n")) }()
		select {
		case <-probe.started:
		case <-time.After(within):
			panic("provider never read the pipe")
		}
		cancel()
		Eventually(done).WithTimeout(within).WithPolling(poll).Should(Receive(MatchError(context.Canceled)))
	})

	// With the context cancelled and a consumer waiting, a send that merely
	// raced the cancellation would win about half the time, so the turn is
	// repeated: any delivery in any round counts.
	It("drops a result held back during the reap when the context is cancelled", func() {
		delivered := 0
		for round := 0; round < 24; round++ {
			gate, reaping := make(chan struct{}), make(chan struct{})
			fake.proc = func(context.Context) *fakeProc {
				return &fakeProc{out: io.NopCloser(strings.NewReader(okEnd + "\n")),
					wait: func() error { close(reaping); <-gate; return nil }}
			}
			ctx, cancel := context.WithCancel(context.Background())
			got := make(chan []provider.StreamEvent, 1)
			ch := p.Stream(ctx, userReq("hi"))
			go func() { got <- drain(ch) }()
			select {
			case <-reaping: // the result is now held back behind the reap
			case <-time.After(within):
				panic("provider never reached the reap")
			}
			cancel()
			close(gate)
			select {
			case evs := <-got:
				delivered += len(evs)
			case <-time.After(within):
				panic("stream never closed")
			}
		}
		Expect(delivered).To(BeZero())
	})

	It("ends in a terminal error when the launcher returns no process and no error", func() {
		q := anthropic.NewSubscription("c", func(context.Context, anthropic.Command) (anthropic.Process, error) {
			return nil, nil
		})
		_, err := q.Chat(context.Background(), userReq("hi"))
		Expect(err).To(MatchError(anthropic.ErrNoProcess))
	})

	DescribeTable("keeps the default line cap when WithMaxLine is not positive",
		func(n int) {
			fake.stdout = `{"type":"result","pad":"` + strings.Repeat("x", 1<<20) + `"}` + "\n"
			_, err := anthropic.NewSubscription("c", fake.launcher(), anthropic.WithMaxLine(n)).
				Chat(context.Background(), userReq("hi"))
			Expect(err).To(Succeed())
		},
		Entry("zero", 0),
		Entry("negative", -1),
	)

	DescribeTable("keeps the default grace period when WithResultGrace is not positive",
		func(grace time.Duration) {
			var killed atomic.Bool
			q := anthropic.NewSubscription("c", func(ctx context.Context, _ anthropic.Command) (anthropic.Process, error) {
				return &fakeProc{out: io.NopCloser(strings.NewReader(okEnd + "\n")), wait: func() error {
					select {
					case <-ctx.Done():
						killed.Store(true)
					case <-time.After(150 * time.Millisecond): // the child exits on its own
					}
					return nil
				}}, nil
			}, anthropic.WithResultGrace(grace))
			_, err := q.Chat(context.Background(), userReq("hi"))
			if err != nil {
				panic(err)
			}
			Expect(killed.Load()).To(BeFalse())
		},
		Entry("zero", time.Duration(0)),
		Entry("negative", -time.Second),
	)

	Describe("holding back the result", func() {
		var (
			ch      <-chan provider.StreamEvent
			release func()
		)

		BeforeEach(func() {
			gate := make(chan struct{})
			release = func() { close(gate) }
			fake.proc = func(context.Context) *fakeProc {
				return &fakeProc{out: io.NopCloser(strings.NewReader(okEnd + "\n")), wait: func() error { <-gate; return nil }}
			}
			ch = p.Stream(context.Background(), userReq("hi"))
		})

		It("delivers nothing while the reap is pending", func() {
			DeferCleanup(release)
			Consistently(ch).WithTimeout(200 * time.Millisecond).WithPolling(poll).ShouldNot(Receive())
		})
		It("delivers the finish event once the reap completes", func() {
			release()
			Eventually(ch).WithTimeout(within).WithPolling(poll).Should(Receive(Equal(finish)))
		})
	})
})
