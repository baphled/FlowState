package main

import (
	"context"
	"encoding/json"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

// crashedDoctorSession creates a session whose event log ends in a
// dangling turn.start with provider traffic — the crash shape a
// killed `session run` leaves behind — panicking on setup errors.
func crashedDoctorSession() string {
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "crash probe")
	if created.exit != 0 {
		panic("session new failed: " + created.stderr)
	}
	id := decodeNew(created.stdout).ID
	st, oerr := app.OpenStore(dbFor())
	if oerr != nil {
		panic(oerr)
	}
	w, werr := st.NewEventWriter(context.Background(), id)
	if werr != nil {
		panic(werr)
	}
	if _, aerr := w.Append(context.Background(), "engine", "provider.request", map[string]any{}); aerr != nil {
		panic(aerr)
	}
	if cerr := w.Close(); cerr != nil {
		panic(cerr)
	}
	if serr := st.Close(); serr != nil {
		panic(serr)
	}
	return id
}

// doctorRecord runs `session doctor` for id against the per-spec DB
// and decodes the first stdout line as JSON.
func doctorRecord(id string) (result, map[string]any) {
	r := runArgv([]string{"session", "doctor", id, "--db", dbFor()})
	var rec map[string]any
	if uerr := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); uerr != nil {
		panic(uerr)
	}
	return r, rec
}

var _ = Describe("Session doctor CLI", func() {
	It("exits 0 for a crashed session", func() {
		id := crashedDoctorSession()
		r, _ := doctorRecord(id)
		Expect(r.exit).To(Equal(0))
	})

	It("prints the full diagnosis record for a crashed session", func() {
		id := crashedDoctorSession()
		_, rec := doctorRecord(id)
		Expect(rec).To(Equal(map[string]any{
			"session_id": id, "status": "active", "crashed": true,
			"run_activity": true, "pending_messages": float64(0),
			"reserved": float64(0), "budget_pool": nil, "available": nil,
		}))
	})

	It("validates the crashed diagnosis against the session-doctor schema", func() {
		id := crashedDoctorSession()
		r, _ := doctorRecord(id)
		verr := validateAgainst("session-doctor", firstLine(r.stdout))
		Expect(verr).NotTo(HaveOccurred())
	})

	It("rejects a diagnosis record missing a required field", func() {
		err := validateAgainst("session-doctor",
			`{"session_id":"01a1f000-0000-4000-8000-000000000000","status":"active",`+
				`"run_activity":true,"pending_messages":0,"reserved":0,"budget_pool":null,"available":null}`)
		Expect(err).To(HaveOccurred())
	})

	It("exits 0 for a paused session", func() {
		id := newPausedSession()
		r, _ := doctorRecord(id)
		Expect(r.exit).To(Equal(0))
	})

	It("reports a paused session as un-crashed", func() {
		id := newPausedSession()
		_, rec := doctorRecord(id)
		Expect(rec["crashed"]).To(BeFalse())
	})

	It("reports the paused row status", func() {
		id := newPausedSession()
		_, rec := doctorRecord(id)
		Expect(rec["status"]).To(Equal("paused"))
	})

	It("exits 0 for a fresh session", func() {
		created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "fresh probe")
		if created.exit != 0 {
			panic("session new failed: " + created.stderr)
		}
		r, _ := doctorRecord(decodeNew(created.stdout).ID)
		Expect(r.exit).To(Equal(0))
	})

	It("reports no run activity for a fresh session", func() {
		created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "fresh probe")
		if created.exit != 0 {
			panic("session new failed: " + created.stderr)
		}
		_, rec := doctorRecord(decodeNew(created.stdout).ID)
		Expect(rec["run_activity"]).To(BeFalse())
	})

	It("exits 1 for an unknown session id", func() {
		db := filepath.Join(GinkgoT().TempDir(), "doctor-unknown.db")
		r := runArgv([]string{"session", "doctor", "01a1f000-0000-4000-8000-000000000000", "--db", db})
		Expect(r.exit).To(Equal(1))
	})

	It("reports stderr code session_not_found for an unknown session id", func() {
		db := filepath.Join(GinkgoT().TempDir(), "doctor-unknown.db")
		r := runArgv([]string{"session", "doctor", "01a1f000-0000-4000-8000-000000000000", "--db", db})
		var e struct {
			Code string `json:"code"`
		}
		if uerr := json.Unmarshal([]byte(firstLine(r.stderr)), &e); uerr != nil {
			panic(uerr)
		}
		Expect(e.Code).To(Equal("session_not_found"))
	})

	It("exits 2 when the session id is missing", func() {
		r := runArgv([]string{"session", "doctor", "--db", dbFor()})
		Expect(r.exit).To(Equal(2))
	})
})
