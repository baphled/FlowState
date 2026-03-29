package external_test

import (
	"bytes"
	"context"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/external"
	"github.com/baphled/flowstate/internal/plugin/manifest"
)

type writeCloser struct {
	*bytes.Buffer
}

func (wc *writeCloser) Close() error {
	return nil
}

var _ = Describe("Spawner and PluginProcess", func() {
	var (
		spawner *external.Spawner
		ctx     context.Context
		cancel  context.CancelFunc
	)

	BeforeEach(func() {
		//nolint:fatcontext
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		spawner = external.NewSpawner()
	})

	AfterEach(func() {
		cancel()
	})

	Describe("NewSpawner", func() {
		It("creates a new Spawner instance", func() {
			s := external.NewSpawner()
			Expect(s).NotTo(BeNil())
		})
	})

	Describe("Spawn", func() {
		It("returns error when manifest Command is empty", func() {
			m := &manifest.Manifest{
				Name: "test",
			}

			proc, err := spawner.Spawn(ctx, m)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("manifest command is empty"))
			Expect(proc).To(BeNil())
		})

		It("returns error when binary does not exist", func() {
			m := &manifest.Manifest{
				Name:    "test",
				Command: "/nonexistent/binary/path",
			}

			proc, err := spawner.Spawn(ctx, m)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("start process"))
			Expect(proc).To(BeNil())
		})

		It("spawns a process that exits immediately", func() {
			m := &manifest.Manifest{
				Name:    "test",
				Command: "true",
			}

			proc, err := spawner.Spawn(ctx, m)
			Expect(err).NotTo(HaveOccurred())
			Expect(proc).NotTo(BeNil())

			Eventually(proc.Done(), 5*time.Second).Should(BeClosed())
		})

		It("creates PluginProcess with working done channel", func() {
			m := &manifest.Manifest{
				Name:    "test",
				Command: "true",
			}

			proc, err := spawner.Spawn(ctx, m)
			Expect(err).NotTo(HaveOccurred())

			doneCh := proc.Done()
			Expect(doneCh).NotTo(BeNil())

			Eventually(doneCh, 5*time.Second).Should(BeClosed())
		})
	})

	Describe("PluginProcess", func() {
		It("NewPluginProcess creates a PluginProcess with streams and done channel", func() {
			r := io.NopCloser(bytes.NewBuffer([]byte("test")))
			w := &writeCloser{Buffer: &bytes.Buffer{}}
			done := make(chan struct{})

			proc := external.NewPluginProcess(r, w, done)
			Expect(proc).NotTo(BeNil())
			Expect(proc.Done()).NotTo(BeNil())
		})

		It("Read reads from the reader", func() {
			testData := []byte("hello world")
			r := io.NopCloser(bytes.NewBuffer(testData))
			w := &writeCloser{Buffer: &bytes.Buffer{}}
			done := make(chan struct{})

			proc := external.NewPluginProcess(r, w, done)

			readBuf := make([]byte, 100)
			n, err := proc.Read(readBuf)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(len(testData)))
			Expect(readBuf[:n]).To(Equal(testData))
		})

		It("Write writes to the writer", func() {
			r := io.NopCloser(bytes.NewBuffer(nil))
			buf := &bytes.Buffer{}
			w := &writeCloser{Buffer: buf}
			done := make(chan struct{})

			proc := external.NewPluginProcess(r, w, done)

			testData := []byte("test message")
			n, err := proc.Write(testData)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(len(testData)))
			Expect(buf.Bytes()).To(Equal(testData))
		})

		It("Done returns a receive-only channel", func() {
			r := io.NopCloser(bytes.NewBuffer(nil))
			w := &writeCloser{Buffer: &bytes.Buffer{}}
			done := make(chan struct{})

			proc := external.NewPluginProcess(r, w, done)

			Expect(proc.Done()).NotTo(BeNil())
		})

		It("Kill closes reader and writer", func() {
			r := io.NopCloser(bytes.NewBuffer(nil))
			w := &writeCloser{Buffer: &bytes.Buffer{}}
			done := make(chan struct{})

			proc := external.NewPluginProcess(r, w, done)

			err := proc.Kill()
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("StopProcess", func() {
		It("returns nil when stopping a process that has exited", func() {
			m := &manifest.Manifest{
				Name:    "test",
				Command: "true",
			}

			proc, err := spawner.Spawn(ctx, m)
			Expect(err).NotTo(HaveOccurred())

			Eventually(proc.Done(), 5*time.Second).Should(BeClosed())

			err = spawner.StopProcess("test", proc)
			Expect(err).NotTo(HaveOccurred())
		})

		It("calls Kill to close streams", func() {
			m := &manifest.Manifest{
				Name:    "test",
				Command: "true",
			}

			proc, err := spawner.Spawn(ctx, m)
			Expect(err).NotTo(HaveOccurred())

			err = spawner.StopProcess("test", proc)
			Expect(err).NotTo(HaveOccurred())
		})

		It("handles nil process.cmd without panic", func() {
			proc := external.NewPluginProcess(nil, nil, make(chan struct{}))

			err := spawner.StopProcess("test", proc)
			Expect(err).NotTo(HaveOccurred())
		})

		It("sends SIGTERM to running process", func() {
			m := &manifest.Manifest{
				Name:    "test",
				Command: "sleep",
				Args:    []string{"10"},
			}

			proc, err := spawner.Spawn(ctx, m)
			Expect(err).NotTo(HaveOccurred())

			err = spawner.StopProcess("test", proc)
			Expect(err).NotTo(HaveOccurred())

			Eventually(proc.Done(), 10*time.Second).Should(BeClosed())
		})
	})
})
