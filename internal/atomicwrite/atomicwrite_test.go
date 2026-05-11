package atomicwrite_test

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/baphled/flowstate/internal/atomicwrite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("atomicwrite.File", func() {
	var (
		tempDir string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "atomicwrite-test-*")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	})

	Context("when writing a new file", func() {
		It("creates the target file with the requested bytes", func() {
			path := filepath.Join(tempDir, "credentials.json")
			payload := []byte(`{"token":"abc"}`)

			err := atomicwrite.File(path, payload, 0o600)

			Expect(err).ToNot(HaveOccurred())
			got, readErr := os.ReadFile(path)
			Expect(readErr).ToNot(HaveOccurred())
			Expect(got).To(Equal(payload))
		})

		It("applies the requested permission bits", func() {
			path := filepath.Join(tempDir, "perm.json")

			err := atomicwrite.File(path, []byte("x"), 0o600)

			Expect(err).ToNot(HaveOccurred())
			info, statErr := os.Stat(path)
			Expect(statErr).ToNot(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})

		It("leaves no temp file behind on success", func() {
			path := filepath.Join(tempDir, "no-leak.json")

			err := atomicwrite.File(path, []byte("x"), 0o600)
			Expect(err).ToNot(HaveOccurred())

			entries, readErr := os.ReadDir(tempDir)
			Expect(readErr).ToNot(HaveOccurred())
			for _, entry := range entries {
				Expect(entry.Name()).ToNot(ContainSubstring(".tmp"))
				Expect(entry.Name()).ToNot(ContainSubstring(".atomicwrite-"))
			}
		})
	})

	Context("when overwriting an existing file", func() {
		It("replaces the contents atomically", func() {
			path := filepath.Join(tempDir, "creds.json")
			Expect(os.WriteFile(path, []byte("old"), 0o600)).To(Succeed())

			err := atomicwrite.File(path, []byte("new"), 0o600)

			Expect(err).ToNot(HaveOccurred())
			got, readErr := os.ReadFile(path)
			Expect(readErr).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("new"))
		})

		It("never produces a zero-byte file when overwriting", func() {
			// The classic non-atomic failure mode: os.WriteFile truncates
			// first then writes. If the process dies between truncate and
			// write the target is zero bytes. atomicwrite.File must never
			// expose that intermediate state at the target path — the file
			// either has the old bytes or the new bytes, never empty.
			path := filepath.Join(tempDir, "creds.json")
			Expect(os.WriteFile(path, []byte(`{"refresh":"r1"}`), 0o600)).To(Succeed())

			err := atomicwrite.File(path, []byte(`{"refresh":"r2"}`), 0o600)
			Expect(err).ToNot(HaveOccurred())

			info, statErr := os.Stat(path)
			Expect(statErr).ToNot(HaveOccurred())
			Expect(info.Size()).To(BeNumerically(">", int64(0)))
		})
	})

	Context("when the parent directory does not exist", func() {
		It("returns an error rather than silently dropping the write", func() {
			path := filepath.Join(tempDir, "missing-dir", "out.json")

			err := atomicwrite.File(path, []byte("x"), 0o600)

			Expect(err).To(HaveOccurred())
			_, statErr := os.Stat(path)
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	Context("when called concurrently against the same path", func() {
		It("never produces a corrupted or zero-byte file", func() {
			// Multiple goroutines all writing to the same path. Whichever
			// rename wins, the final file must be a complete payload from
			// one of them — not an empty file, not a partial mix.
			path := filepath.Join(tempDir, "concurrent.json")
			payload := []byte(`{"token":"value"}`)

			var wg sync.WaitGroup
			for range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					Expect(atomicwrite.File(path, payload, 0o600)).To(Succeed())
				}()
			}
			wg.Wait()

			got, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(payload))
		})
	})
})
