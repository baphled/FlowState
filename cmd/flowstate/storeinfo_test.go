package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// storeInfoRecord creates the per-spec database and runs `store info`
// against it, returning the result and the decoded first stdout line.
func storeInfoRecord() (result, map[string]any) {
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "info probe")
	if created.exit != 0 {
		panic("session new failed: " + created.stderr)
	}
	r := runArgv([]string{"store", "info", "--db", dbFor()})
	var rec map[string]any
	if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
		panic(err)
	}
	return r, rec
}

var _ = Describe("Store info CLI", func() {
	It("exits 0 for a current database", func() {
		r, _ := storeInfoRecord()
		Expect(r.exit).To(Equal(0))
	})

	It("reports schema version 9", func() {
		_, rec := storeInfoRecord()
		Expect(rec["schema_version"]).To(Equal(float64(9)))
	})

	It("reports that the handle enforces foreign keys", func() {
		_, rec := storeInfoRecord()
		Expect(rec["foreign_keys_enforced"]).To(BeTrue())
	})

	It("echoes the --db path", func() {
		_, rec := storeInfoRecord()
		Expect(rec["path"]).To(Equal(dbFor()))
	})

	It("conforms to the store-info schema", func() {
		r, _ := storeInfoRecord()
		Expect(validateAgainst("store-info", firstLine(r.stdout))).To(Succeed())
	})

	It("refuses a missing database with the usage exit", func() {
		r := runArgv([]string{"store", "info", "--db", dbFor()})
		Expect(r.exit).To(Equal(2))
	})

	It("reports the usage code on standard error for a missing database", func() {
		r := runArgv([]string{"store", "info", "--db", dbFor()})
		Expect(r.stderr).To(ContainSubstring(`"code":"usage"`))
	})

	It("creates no file for a missing database", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "absent.db")
		runArgv([]string{"store", "info", "--db", missing})
		_, err := os.Stat(missing)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("refuses an unknown store subcommand", func() {
		r := runArgv([]string{"store", "frobnicate"})
		Expect(r.exit).To(Equal(2))
	})

	It("refuses a stray argument for a database that exists", func() {
		storeInfoRecord()
		r := runArgv([]string{"store", "info", "extra", "--db", dbFor()})
		Expect(r.exit).To(Equal(2))
	})

	It("refuses an empty --db as a usage error", func() {
		r := runArgv([]string{"store", "info", "--db", ""})
		Expect(r.stderr).To(ContainSubstring("no database path given"))
	})

	It("reads the database named by storage_path in --config", func() {
		storeInfoRecord()
		cfg := e2eWriteFlowstateConfig(GinkgoT().TempDir(), fmt.Sprintf(`{"storage_path":%q}`, dbFor()))
		r := runArgv([]string{"store", "info", "--config", cfg})
		var rec map[string]any
		if err := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); err != nil {
			panic(err)
		}
		Expect(rec["path"]).To(Equal(dbFor()))
	})

	It("lets --db override storage_path in --config", func() {
		storeInfoRecord()
		absent := filepath.Join(GinkgoT().TempDir(), "absent.db")
		cfg := e2eWriteFlowstateConfig(GinkgoT().TempDir(), fmt.Sprintf(`{"storage_path":%q}`, absent))
		r := runArgv([]string{"store", "info", "--config", cfg, "--db", dbFor()})
		Expect(r.exit).To(Equal(0))
	})

	It("exits 4 on a malformed --config", func() {
		cfg := e2eWriteFlowstateConfig(GinkgoT().TempDir(), `{"storage_path":`)
		r := runArgv([]string{"store", "info", "--config", cfg})
		Expect(r.exit).To(Equal(4))
	})

	It("lists the command in the help text", func() {
		r := runArgv([]string{"--help"})
		Expect(r.stdout).To(ContainSubstring("store info"))
	})
})
