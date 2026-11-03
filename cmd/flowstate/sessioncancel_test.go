package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("session cancel and SIGINT", func() {
	var dir, db string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		db = filepath.Join(dir, "test.db")
	})

	// newTestSession creates an active session and returns its id.
	newTestSession := func() (string, error) {
		res := runArgv([]string{"session", "new", "--db", db,
			"--provider", "zai", "--model", "glm-5.3", "--task", "hi"})
		if res.exit != 0 {
			return "", errors.New(res.stderr)
		}
		var rec newRecord
		if err := json.Unmarshal([]byte(firstLine(res.stdout)), &rec); err != nil {
			return "", err
		}
		return rec.ID, nil
	}

	It("exits 2 without a session id", func() {
		Expect(runArgv([]string{"session", "cancel", "--db", db}).exit).To(Equal(2))
	})

	It("exits 1 on an unknown session", func() {
		Expect(runArgv([]string{"session", "cancel", "--db", db, "nope"}).exit).To(Equal(1))
	})

	It("cancels an active session with exit 0", func() {
		id, newErr := newTestSession()
		Expect(newErr).NotTo(HaveOccurred())
		Expect(runArgv([]string{"session", "cancel", "--db", db, id}).exit).To(Equal(0))
	})

	It("exits 6 cancelling a cancelled session", func() {
		id, newErr := newTestSession()
		Expect(newErr).NotTo(HaveOccurred())
		_ = runArgv([]string{"session", "cancel", "--db", db, id})
		Expect(runArgv([]string{"session", "cancel", "--db", db, id}).exit).To(Equal(6))
	})

	It("exits 130 on the SIGINT cancel path", func() {
		id, newErr := newTestSession()
		Expect(newErr).NotTo(HaveOccurred())
		ctxMarked := contextWithSigint()
		outR, outW, _ := os.Pipe()
		errR, errW, _ := os.Pipe()
		_ = outW.Close()
		exit := dispatchContext(ctxMarked, []string{"session", "cancel", id, "--db", db}, outW, errW)
		_ = errW.Close()
		_ = outR.Close()
		_ = errR.Close()
		Expect(exit).To(Equal(130))
	})
})
