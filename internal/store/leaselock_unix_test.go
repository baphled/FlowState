//go:build !windows

package store

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const lockFdScanEnv = "FLOWSTATE_LOCK_FDSCAN"

// init turns the binary into a descriptor scanner: it exits 9 when any
// descriptor from 3 up is the file named by the environment variable.
func init() {
	target := os.Getenv(lockFdScanEnv)
	if target == "" {
		return
	}
	var want syscall.Stat_t
	if err := syscall.Stat(target, &want); err != nil {
		os.Exit(1)
	}
	for fd := 3; fd < 256; fd++ {
		var got syscall.Stat_t
		if syscall.Fstat(fd, &got) == nil && got.Dev == want.Dev && got.Ino == want.Ino {
			os.Exit(9)
		}
	}
	os.Exit(0)
}

var _ = Describe("lease lock primitive on Unix", func() {
	var db string

	BeforeEach(func() { db = newLockDB() })

	It("refuses the same file opened through a symlink", func() {
		mustLock(db, "exec:1")
		link := filepath.Join(GinkgoT().TempDir(), "link.db")
		if err := os.Symlink(db, link); err != nil {
			panic(err)
		}
		_, err := AcquireItemLock(nil, link, "exec:1")
		Expect(err).To(MatchError(ErrLockHeld))
	})

	It("refuses a child that names the file through a symlink", func() {
		mustLock(db, "exec:1")
		link := filepath.Join(GinkgoT().TempDir(), "link.db")
		if err := os.Symlink(db, link); err != nil {
			panic(err)
		}
		Expect(lockChildExit("try", link, "exec:1")).To(Equal(childHeld))
	})

	It("frees the lock when the holding process is killed", func() {
		cmd := holdChild(db, "exec:1")
		if lockChildExit("try", db, "exec:1") != childHeld {
			panic("holder does not hold the lock")
		}
		if err := cmd.Process.Kill(); err != nil {
			panic(err)
		}
		_ = cmd.Wait()
		Expect(lockChildExit("try", db, "exec:1")).To(Equal(0))
	})

	It("does not leak the descriptor into a child process (close-on-exec)", func() {
		mustLock(db, "exec:1")
		id, err := ResolveLockIdentity(db)
		if err != nil {
			panic(err)
		}
		dir, fan, name := LockPath(id, "exec:1")
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), lockFdScanEnv+"="+filepath.Join(dir, fan, name))
		Expect(cmd.Run()).To(Succeed())
	})
})

// replaceLockFile removes and recreates the lock file, as a hostile or racing
// process would.
func replaceLockFile(root *os.Root, name string) {
	if err := root.Remove(name); err != nil {
		panic(err)
	}
	g, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		panic(err)
	}
	_ = g.Close()
}

// openFDCount counts this process's open descriptors through /proc.
func openFDCount() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		panic(err)
	}
	return len(entries)
}

var _ = Describe("lease lock replacement and hostile files on Unix", func() {
	var db string

	BeforeEach(func() {
		db = newLockDB()
		DeferCleanup(func(orig func(*os.Root, string)) { lockAfterOpenHook = orig }, lockAfterOpenHook)
	})

	It("retries after the lock file is replaced once and then locks", func() {
		calls := 0
		lockAfterOpenHook = func(root *os.Root, name string) {
			calls++
			if calls == 1 {
				replaceLockFile(root, name)
			}
		}
		l, err := AcquireItemLock(nil, db, "exec:1")
		if err == nil {
			DeferCleanup(func() { _ = l.Release() })
		}
		Expect(l).NotTo(BeNil())
	})

	It("checks the replaced file a second time", func() {
		calls := 0
		lockAfterOpenHook = func(root *os.Root, name string) {
			calls++
			if calls == 1 {
				replaceLockFile(root, name)
			}
		}
		l, err := AcquireItemLock(nil, db, "exec:1")
		if err == nil {
			DeferCleanup(func() { _ = l.Release() })
		}
		Expect(calls).To(Equal(2))
	})

	It("reports ErrLockUnstable when the file is replaced on every attempt", func() {
		lockAfterOpenHook = replaceLockFile
		_, err := AcquireItemLock(nil, db, "exec:1")
		Expect(errors.Is(err, ErrLockUnstable)).To(BeTrue())
	})

	It("gives up after the bounded number of attempts", func() {
		calls := 0
		lockAfterOpenHook = func(root *os.Root, name string) {
			calls++
			replaceLockFile(root, name)
		}
		_, _ = AcquireItemLock(nil, db, "exec:1")
		Expect(calls).To(Equal(maxLockAttempts))
	})

	It("does not report a held lock as unstable", func() {
		mustLock(db, "exec:1")
		_, err := AcquireItemLock(nil, db, "exec:1")
		Expect(errors.Is(err, ErrLockUnstable)).To(BeFalse())
	})

	It("refuses a FIFO at the lock path without blocking", func() {
		id, err := ResolveLockIdentity(db)
		if err != nil {
			panic(err)
		}
		dir, fan, name := LockPath(id, "exec:1")
		if err := os.MkdirAll(filepath.Join(dir, fan), 0o750); err != nil {
			panic(err)
		}
		if err := syscall.Mkfifo(filepath.Join(dir, fan, name), 0o600); err != nil {
			panic(err)
		}
		result := make(chan error, 1)
		go func() {
			_, err := AcquireItemLock(nil, db, "exec:1")
			result <- err
		}()
		Eventually(result).WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).
			Should(Receive(MatchError(ErrLockNotRegular)))
	})

	if runtime.GOOS == "linux" {
		It("closes the descriptor on release", func() {
			l, err := AcquireItemLock(nil, db, "exec:warm")
			if err != nil {
				panic(err)
			}
			_ = l.Release()
			before := openFDCount()
			for i := 0; i < 200; i++ {
				l, err := AcquireItemLock(nil, db, "exec:1")
				if err != nil {
					panic(err)
				}
				if err := l.Release(); err != nil {
					panic(err)
				}
			}
			Expect(openFDCount() - before).To(BeNumerically("<", 100))
		})
	}
})

// holdChild starts a child that holds the lock and returns it once it says so.
func holdChild(db, item string) *exec.Cmd {
	cmd := lockChild("hold", db, item)
	in, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "locked\n" {
		panic("child did not lock")
	}
	return cmd
}
