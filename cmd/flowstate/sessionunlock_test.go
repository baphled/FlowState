package main

import (
	"context"
	"encoding/json"
	"strings"

	sqlite "github.com/libtnb/sqlite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/store"
)

// runUnlock runs `session unlock` with args against the per-spec database.
func runUnlock(args ...string) result {
	return runArgv(append(append([]string{"session", "unlock"}, args...), "--db", dbFor()))
}

// seedLeaseFor inserts the exec lease row of session id through the mapped table, as a run of
// another binary, another machine or this one (a dead one, when its lock is free) would have left it.
func seedLeaseFor(id, owner, machine, host string, pid int64) {
	st, err := app.OpenStore(dbFor())
	must(err)
	defer func() { must(st.Close()) }()
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: st.DB()}), &gorm.Config{Logger: logger.Discard})
	must(err)
	must(g.Table("leases").Create(map[string]any{"item": "exec:" + id, "agent": owner, "token": 1,
		"acquired_at_utc": "2026-01-02T03:04:05.000Z", "machine": machine, "host": host, "pid": pid}).Error)
}

// leaseRowFor reports whether session id still has its exec lease row.
func leaseRowFor(id string) bool {
	st, err := app.OpenStore(dbFor())
	must(err)
	defer func() { must(st.Close()) }()
	_, found, err := st.ReadLease(context.Background(), "exec:"+id)
	must(err)
	return found
}

// unlockedEventCount counts the session.unlocked events in the session's log.
func unlockedEventCount(id string) int {
	return strings.Count(runCLIEvents(id).stdout, "session.unlocked")
}

// unlockField decodes one top-level field of the JSON a run printed.
func unlockField(out, key string) any {
	var m map[string]any
	must(json.Unmarshal([]byte(out), &m))
	return m[key]
}

// holdLease takes the run lease of session id from a second store handle: a live holder, whose
// lock is held in this process.
func holdLease(id string) {
	st, err := app.OpenStore(dbFor())
	must(err)
	h, err := st.AcquireRunLease(context.Background(), "exec:"+id, "run:live")
	must(err)
	DeferCleanup(func() {
		must(h.Release(context.Background()))
		must(st.Close())
	})
}

var _ = Describe("session unlock", func() {
	var id, machine string

	BeforeEach(func() {
		machine = store.MachineIdentity()
		if machine == "" {
			panic("no machine identity")
		}
		id = seedCrashSession(dbFor(), func(*store.EventWriter) {})
	})

	Context("when the session has no lease row", func() {
		It("exits 0", func() { Expect(runUnlock(id).exit).To(Equal(0)) })
		It("reports the outcome no_lease", func() {
			Expect(unlockField(runUnlock(id).stdout, "outcome")).To(Equal("no_lease"))
		})
		It("prints a report that matches the session-unlock schema", func() {
			Expect(validateAgainst("session-unlock", runUnlock(id).stdout)).To(Succeed())
		})
		It("writes no event", func() {
			runUnlock(id, "--force")
			Expect(unlockedEventCount(id)).To(Equal(0))
		})
	})

	Context("without --force, on the row of another machine", func() {
		BeforeEach(func() { seedLeaseFor(id, "run:abc", "other-machine", "elsewhere", 4242) })

		It("exits 1", func() { Expect(runUnlock(id).exit).To(Equal(1)) })
		It("keeps the row", func() {
			runUnlock(id)
			Expect(leaseRowFor(id)).To(BeTrue())
		})
		It("prints the holder's owner", func() {
			Expect(runUnlock(id).stdout).To(ContainSubstring(`"owner":"run:abc"`))
		})
		It("prints the holder's host", func() {
			Expect(runUnlock(id).stdout).To(ContainSubstring(`"host":"elsewhere"`))
		})
		It("prints the holder's pid", func() {
			Expect(runUnlock(id).stdout).To(ContainSubstring(`"pid":4242`))
		})
		It("reports the outcome reported", func() {
			Expect(unlockField(runUnlock(id).stdout, "outcome")).To(Equal("reported"))
		})
		It("prints a report that matches the session-unlock schema", func() {
			Expect(validateAgainst("session-unlock", runUnlock(id).stdout)).To(Succeed())
		})
		It("writes no event", func() {
			runUnlock(id)
			Expect(unlockedEventCount(id)).To(Equal(0))
		})
		It("says on stderr that the row was not deleted", func() {
			Expect(runUnlock(id).stderr).To(ContainSubstring("lease not deleted"))
		})
	})

	Context("with --force, on the row of another machine", func() {
		BeforeEach(func() { seedLeaseFor(id, "run:abc", "other-machine", "elsewhere", 4242) })

		It("exits 0", func() { Expect(runUnlock(id, "--force").exit).To(Equal(0)) })
		It("deletes the row", func() {
			runUnlock(id, "--force")
			Expect(leaseRowFor(id)).To(BeFalse())
		})
		It("appends one session.unlocked event", func() {
			runUnlock(id, "--force")
			Expect(unlockedEventCount(id)).To(Equal(1))
		})
		It("reports the outcome deleted", func() {
			Expect(unlockField(runUnlock(id, "--force").stdout, "outcome")).To(Equal("deleted"))
		})
		It("reports that the machine does not match", func() {
			Expect(unlockField(runUnlock(id, "--force").stdout, "machine_matches")).To(BeFalse())
		})
		It("prints a report that matches the session-unlock schema", func() {
			Expect(validateAgainst("session-unlock", runUnlock(id, "--force").stdout)).To(Succeed())
		})
		It("writes exactly one stderr line", func() {
			Expect(strings.Count(runUnlock(id, "--force").stderr, "\n")).To(Equal(1))
		})
		It("names the owner on the stderr line", func() {
			Expect(runUnlock(id, "--force").stderr).To(ContainSubstring("run:abc"))
		})
		It("names the host on the stderr line", func() {
			Expect(runUnlock(id, "--force").stderr).To(ContainSubstring("elsewhere"))
		})
		It("names the pid on the stderr line", func() {
			Expect(runUnlock(id, "--force").stderr).To(ContainSubstring("pid 4242"))
		})
		It("lets a following acquire succeed", func() {
			runUnlock(id, "--force")
			st, err := app.OpenStore(dbFor())
			must(err)
			defer func() { must(st.Close()) }()
			h, err := st.AcquireRunLease(context.Background(), "exec:"+id, "run:next")
			must(err)
			defer func() { must(h.Release(context.Background())) }()
			Expect(h.Owner()).To(Equal("run:next"))
		})
	})

	Context("with --force, on a dead holder of this machine", func() {
		BeforeEach(func() { seedLeaseFor(id, "run:dead", machine, "here", 1) })

		It("exits 0", func() { Expect(runUnlock(id, "--force").exit).To(Equal(0)) })
		It("deletes the row", func() {
			runUnlock(id, "--force")
			Expect(leaseRowFor(id)).To(BeFalse())
		})
		It("appends one session.unlocked event", func() {
			runUnlock(id, "--force")
			Expect(unlockedEventCount(id)).To(Equal(1))
		})
		It("reports that the machine matches", func() {
			Expect(unlockField(runUnlock(id, "--force").stdout, "machine_matches")).To(BeTrue())
		})
	})

	Context("without --force, on a dead holder of this machine", func() {
		BeforeEach(func() { seedLeaseFor(id, "run:dead", machine, "here", 1) })

		It("exits 1", func() { Expect(runUnlock(id).exit).To(Equal(1)) })
		It("keeps the row", func() {
			runUnlock(id)
			Expect(leaseRowFor(id)).To(BeTrue())
		})
	})

	Context("with --force, on a legacy row", func() {
		BeforeEach(func() { seedLeaseFor(id, "cli", "", "", 0) })

		It("exits 0", func() { Expect(runUnlock(id, "--force").exit).To(Equal(0)) })
		It("deletes the row", func() {
			runUnlock(id, "--force")
			Expect(leaseRowFor(id)).To(BeFalse())
		})
		It("reports a legacy row", func() {
			Expect(unlockField(runUnlock(id, "--force").stdout, "legacy_row")).To(BeTrue())
		})
		It("warns on stderr that the run may still be alive", func() {
			Expect(runUnlock(id, "--force").stderr).To(ContainSubstring("if its run is still alive"))
		})
	})

	Context("with --force, on a row of another machine that is not legacy", func() {
		BeforeEach(func() { seedLeaseFor(id, "run:abc", "other-machine", "elsewhere", 4242) })

		It("does not warn about a legacy row", func() {
			Expect(runUnlock(id, "--force").stderr).NotTo(ContainSubstring("legacy"))
		})
	})

	Context("when a live process holds the lease", func() {
		BeforeEach(func() { holdLease(id) })

		It("refuses --force with exit 1", func() { Expect(runUnlock(id, "--force").exit).To(Equal(1)) })
		It("keeps the row", func() {
			runUnlock(id, "--force")
			Expect(leaseRowFor(id)).To(BeTrue())
		})
		It("appends no event", func() {
			runUnlock(id, "--force")
			Expect(unlockedEventCount(id)).To(Equal(0))
		})
		It("prints the holder's owner", func() {
			Expect(runUnlock(id, "--force").stdout).To(ContainSubstring(`"owner":"run:live"`))
		})
		It("names the live holder on stderr", func() {
			Expect(runUnlock(id, "--force").stderr).To(ContainSubstring("live process"))
		})
		It("exits 1 without --force too", func() { Expect(runUnlock(id).exit).To(Equal(1)) })
	})

	Context("with a terminal session", func() {
		BeforeEach(func() {
			runCLIClose(id)
			seedLeaseFor(id, "run:abc", "other-machine", "elsewhere", 4242)
		})

		It("deletes the row", func() {
			runUnlock(id, "--force")
			Expect(leaseRowFor(id)).To(BeFalse())
		})
		It("appends no event", func() {
			runUnlock(id, "--force")
			Expect(unlockedEventCount(id)).To(Equal(0))
		})
		It("exits 0", func() { Expect(runUnlock(id, "--force").exit).To(Equal(0)) })
	})

	Context("with a control character in the owner", func() {
		BeforeEach(func() { seedLeaseFor(id, "run:\x1b[31mred", "other-machine", "elsewhere", 4242) })

		It("escapes it in stdout", func() {
			Expect(runUnlock(id).stdout).NotTo(ContainSubstring("\x1b"))
		})
		It("writes the escape sequence as \\u001b in stdout", func() {
			Expect(runUnlock(id).stdout).To(ContainSubstring(`\u001b[31m`))
		})
		It("escapes it on the stderr line of a forced unlock", func() {
			Expect(runUnlock(id, "--force").stderr).NotTo(ContainSubstring("\x1b"))
		})
		It("writes the escape sequence as \\u001b on the stderr line", func() {
			Expect(runUnlock(id, "--force").stderr).To(ContainSubstring(`\u001b[31m`))
		})
	})

	Context("with a bidi override in the owner, which encoding/json leaves alone", func() {
		BeforeEach(func() { seedLeaseFor(id, "run:\u202egnp", "other-machine", "elsewhere", 4242) })

		It("escapes it in stdout", func() {
			Expect(runUnlock(id).stdout).NotTo(ContainSubstring("\u202e"))
		})
		It("writes it as \\u202e in stdout", func() {
			Expect(runUnlock(id).stdout).To(ContainSubstring(`\u202e`))
		})
		It("escapes it on the stderr line of a forced unlock", func() {
			Expect(runUnlock(id, "--force").stderr).NotTo(ContainSubstring("\u202e"))
		})
	})

	It("exits 2 without a session id", func() { Expect(runUnlock().exit).To(Equal(2)) })
	It("exits 1 for an unknown session", func() { Expect(runUnlock("no-such-session").exit).To(Equal(1)) })
	It("names code session_not_found for an unknown session", func() {
		Expect(runUnlock("no-such-session").stderr).To(ContainSubstring("session_not_found"))
	})

	DescribeTable("refuses a flag that does not apply with exit 2",
		func(args ...string) { Expect(runUnlock(append([]string{id}, args...)...).exit).To(Equal(2)) },
		Entry("--budget", "--budget", "5"),
		Entry("--allow", "--allow", "echo"),
		Entry("--model", "--model", "m"),
		Entry("--instructions", "--instructions", "x"),
		Entry("--follow", "--follow"),
	)

	It("names the first inapplicable flag in the fixed order", func() {
		Expect(runUnlock(id, "--budget", "5", "--model", "m").stderr).To(ContainSubstring("--model"))
	})
	It("does not delete when it refuses a flag", func() {
		seedLeaseFor(id, "run:abc", "other-machine", "elsewhere", 4242)
		runUnlock(id, "--force", "--budget", "5")
		Expect(leaseRowFor(id)).To(BeTrue())
	})
	It("refuses --force on another session command with exit 2", func() {
		Expect(runArgv([]string{"session", "close", id, "--force", "--db", dbFor()}).exit).To(Equal(2))
	})
})
