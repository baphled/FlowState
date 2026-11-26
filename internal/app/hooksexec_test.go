package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func mkHookFifo(dir, name string) string {
	path := filepath.Join(dir, name)
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		panic(string(out))
	}
	return path
}

func chmodScript(dir string, mode os.FileMode) (name, base string) {
	mustHook(os.Chmod(putHookScript(dir, "w"), mode))
	return "./w", dir
}

type execCase func(dir string) (name, base string)

func resolveEntries() []TableEntry {
	resolves := []TableEntry{
		Entry("a relative one against the base directory", func(dir string) (string, string, string) {
			return "./scripts/g", dir, putHookScript(filepath.Join(dir, "scripts"), "g")
		}),
		Entry("an absolute path outside the base directory", func(dir string) (string, string, string) {
			script := putHookScript(GinkgoT().TempDir(), "abs")
			return script, dir, script
		}),
	}
	if runtime.GOOS != "windows" { // Windows needs a .exe match for a bare name
		resolves = append(resolves, Entry("a bare name on PATH", func(dir string) (string, string, string) {
			script := putHookScript(dir, "hook-guard")
			GinkgoT().Setenv("PATH", dir)
			return "hook-guard", "/elsewhere", script
		}))
	}
	return resolves
}

var _ = Describe("Hook executable and file checks", func() {
	var dir string

	BeforeEach(func() { dir = GinkgoT().TempDir() })

	DescribeTable("resolves an executable to its real path",
		func(mk func(dir string) (name, base, script string)) {
			name, base, script := mk(dir)
			got, err := checkExecutable(name, base)
			mustHook(err)
			want, err := filepath.EvalSymlinks(script)
			mustHook(err)
			Expect(got).To(Equal(want))
		},
		resolveEntries(),
	)

	DescribeTable("refuses an executable that is",
		func(mk execCase, want string) {
			name, base := mk(dir)
			_, err := checkExecutable(name, base)
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("missing", func(dir string) (string, string) { return "./nope", dir }, ""),
		Entry("a bare name not on PATH", func(dir string) (string, string) {
			GinkgoT().Setenv("PATH", dir)
			return "hook-guard", dir
		}, ""),
		Entry("a directory", func(dir string) (string, string) {
			mustHook(os.Mkdir(filepath.Join(dir, "d"), 0o700))
			return "./d", dir
		}, "not a regular file"),
	)

	It("reports a missing hook file as absent", func() {
		_, found, err := readHookFile(filepath.Join(dir, "nope", "hooks.json"))
		mustHook(err)
		Expect(found).To(BeFalse())
	})

	It("refuses an oversize hook file", func() {
		path := filepath.Join(dir, "hooks.json")
		mustHook(os.WriteFile(path, make([]byte, MaxHooksFileBytes+1), 0o600))
		_, _, err := readHookFile(path)
		Expect(err).To(MatchError(ContainSubstring("larger than")))
	})

	if runtime.GOOS != "windows" {
		DescribeTable("LoadHooks refuses a hook directory that is not a directory, promptly",
			func(scope HookScope, mk func(parent, name string)) {
				GinkgoT().Setenv("HOME", dir)
				GinkgoT().Setenv("XDG_CONFIG_HOME", dir)
				parent, name := dir, ".flowstate"
				if scope == HookScopeUser {
					cfg, err := os.UserConfigDir()
					mustHook(err)
					mustHook(os.MkdirAll(cfg, 0o700))
					parent, name = cfg, "flowstate"
				}
				mk(parent, name)
				done := make(chan error, 1)
				go func() {
					_, err := LoadHooks(HooksRequest{Scope: scope, ProjectRoot: dir})
					done <- err
				}()
				Eventually(done).WithTimeout(5 * time.Second).WithPolling(10 * time.Millisecond).
					Should(Receive(MatchError(And(ContainSubstring("config_invalid"),
						ContainSubstring("hook directory is not a directory")))))
			},
			Entry("a FIFO, project scope", HookScopeProject, func(p, n string) { mkHookFifo(p, n) }),
			Entry("a FIFO, user scope", HookScopeUser, func(p, n string) { mkHookFifo(p, n) }),
			Entry("a regular file", HookScopeProject, func(parent, name string) {
				mustHook(os.WriteFile(filepath.Join(parent, name), nil, 0o600))
			}),
			Entry("a symlink to a FIFO", HookScopeProject, func(parent, name string) {
				mkHookFifo(parent, "pipe")
				mustHook(os.Symlink("pipe", filepath.Join(parent, name)))
			}),
		)

		DescribeTable("refuses a non-regular hook file promptly",
			func(mk func(dir string) string) {
				path := mk(dir)
				done := make(chan error, 1)
				go func() {
					_, _, err := readHookFile(path)
					done <- err
				}()
				Eventually(done).WithTimeout(5 * time.Second).WithPolling(10 * time.Millisecond).
					Should(Receive(MatchError(ContainSubstring("not a regular file"))))
			},
			Entry("a FIFO", func(dir string) string { return mkHookFifo(dir, "hooks.json") }),
			Entry("a directory", func(dir string) string {
				mustHook(os.Mkdir(filepath.Join(dir, "hooks.json"), 0o700))
				return filepath.Join(dir, "hooks.json")
			}),
			Entry("a symlink to a FIFO", func(dir string) string {
				mkHookFifo(dir, "pipe")
				mustHook(os.Symlink("pipe", filepath.Join(dir, "hooks.json")))
				return filepath.Join(dir, "hooks.json")
			}),
		)

		DescribeTable("refuses an executable that is",
			func(mk execCase, want string) {
				name, base := mk(dir)
				_, err := checkExecutable(name, base)
				Expect(err).To(MatchError(ContainSubstring(want)))
			},
			Entry("group-writable", func(dir string) (string, string) { return chmodScript(dir, 0o770) }, "writable"),
			Entry("world-writable", func(dir string) (string, string) { return chmodScript(dir, 0o707) }, "writable"),
			Entry("without the execute bit", func(dir string) (string, string) {
				putHookFile(dir, "plain", "x")
				return "./plain", dir
			}, "execute bit"),
			Entry("a relative symlink resolving outside the base directory", func(dir string) (string, string) {
				mustHook(os.Symlink(putHookScript(GinkgoT().TempDir(), "out"), filepath.Join(dir, "lnk")))
				return "./lnk", dir
			}, "outside"),
			Entry("a relative path climbing out of the base directory", func(dir string) (string, string) {
				putHookScript(dir, "up")
				mustHook(os.Mkdir(filepath.Join(dir, "base"), 0o700))
				return "../up", filepath.Join(dir, "base")
			}, "outside"),
		)

		DescribeTable("refuses a hook file that is",
			func(mk func(dir string) string, want string) {
				_, _, err := readHookFile(mk(dir))
				Expect(err).To(MatchError(ContainSubstring(want)))
			},
			Entry("group-writable", func(dir string) string {
				return putHookMode(dir, "hooks.json", "{}", 0o660)
			}, "writable"),
			Entry("a symlink to outside its directory", func(dir string) string {
				outside := putHookFile(GinkgoT().TempDir(), "hooks.json", "{}")
				path := filepath.Join(dir, "hooks.json")
				mustHook(os.Symlink(outside, path))
				return path
			}, ""),
		)

		It("reads a hook file that is a symlink inside its directory", func() {
			putHookFile(dir, "real.json", "{}")
			path := filepath.Join(dir, "hooks.json")
			mustHook(os.Symlink("real.json", path))
			data, _, err := readHookFile(path)
			mustHook(err)
			Expect(string(data)).To(Equal("{}"))
		})

		hd := func() string { return filepath.Join(dir, ".flowstate") }
		loadArgv0 := func(argv0 string) (LoadedHooks, error) {
			putHookFile(hd(), "hooks.json", hookFileWith(`{"id":"gate","point":"tool.before","kind":"command","argv":["`+argv0+`"]}`))
			return LoadHooks(HooksRequest{Scope: HookScopeProject, ProjectRoot: dir, Accepted: allHookPoints()})
		}
		badArgv0 := func() []TableEntry {
			return []TableEntry{
				Entry("missing", func() string { return "./absent.sh" }),
				Entry("group-writable", func() string { return filepath.Base(putHookMode(hd(), "w.sh", "#!/bin/sh\n", 0o770)) }),
				Entry("not executable", func() string { return filepath.Base(putHookMode(hd(), "n.sh", "#!/bin/sh\n", 0o600)) }),
				Entry("a symlink out of the hook directory", func() string {
					out := GinkgoT().TempDir()
					mustHook(os.MkdirAll(hd(), 0o700))
					mustHook(os.Symlink(putHookScript(out, "o.sh"), filepath.Join(hd(), "l.sh")))
					return "l.sh"
				}),
			}
		}
		DescribeTable("LoadHooks refuses an argv[0] that is",
			func(mk func() string) {
				_, err := loadArgv0("./" + strings.TrimPrefix(mk(), "./"))
				Expect(err).To(MatchError(ErrConfigInvalid))
			}, badArgv0())
		DescribeTable("LoadHooks names the hook when it refuses an argv[0] that is",
			func(mk func() string) {
				_, err := loadArgv0("./" + strings.TrimPrefix(mk(), "./"))
				Expect(err).To(MatchError(ContainSubstring("gate")))
			}, badArgv0())
		It("LoadHooks records a valid hook's Executable as the resolved path", func() {
			want, err := filepath.EvalSymlinks(putHookScript(hd(), "ok.sh"))
			mustHook(err)
			got, err := loadArgv0("./ok.sh")
			mustHook(err)
			Expect(got.Hooks[0].Executable).To(Equal(want))
		})
	}
})
