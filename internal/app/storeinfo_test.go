package app

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

var _ = Describe("ReadStoreInfo", func() {
	var path string
	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "info.db")
		st, err := OpenStore(path)
		if err != nil {
			panic(err)
		}
		if err := st.Close(); err != nil {
			panic(err)
		}
	})

	It("echoes the inspected path", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.Path).To(Equal(path))
	})

	It("reports the Go toolchain version", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.GoVersion).To(Equal(runtime.Version()))
	})

	It("names the SQLite driver module", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.Driver).To(Equal("modernc.org/sqlite"))
	})

	It("reports a driver version", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.DriverVersion).NotTo(BeEmpty())
	})

	It("reads a module's version from the build info", func() {
		bi := &debug.BuildInfo{Deps: []*debug.Module{{Path: "a/b", Version: "v1.2.3"}}}
		Expect(versionIn(bi, "a/b")).To(Equal("v1.2.3"))
	})

	It("reports unknown for a module the build info lacks", func() {
		Expect(versionIn(&debug.BuildInfo{}, "a/b")).To(Equal("unknown"))
	})

	It("reports the engine version", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.SQLiteVersion).NotTo(BeEmpty())
	})

	It("reports schema version 10", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.SchemaVersion).To(Equal(int64(10)))
	})

	It("reports that the handle enforces foreign keys", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.ForeignKeysEnforced).To(BeTrue())
	})

	It("reports the journal mode", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.JournalMode).To(Equal("wal"))
	})

	It("lists the tables with their foreign key counts", func() {
		info, _ := ReadStoreInfo(path)
		Expect(info.Tables).To(ContainElement(StoreTableInfo{Name: "events", ForeignKeys: 1}))
	})

	It("refuses a missing database as a usage error", func() {
		_, err := ReadStoreInfo(filepath.Join(GinkgoT().TempDir(), "absent.db"))
		Expect(ErrorCode(err)).To(Equal(CodeUsage))
	})

	It("refuses an empty path as a usage error", func() {
		_, err := ReadStoreInfo("")
		Expect(ErrorCode(err)).To(Equal(CodeUsage))
	})

	It("says no database path was given for an empty path", func() {
		_, err := ReadStoreInfo("")
		Expect(err).To(MatchError(ContainSubstring("no database path given")))
	})

	It("wraps the store's missing database error", func() {
		_, err := ReadStoreInfo(filepath.Join(GinkgoT().TempDir(), "absent.db"))
		Expect(err).To(MatchError(store.ErrNoDatabase))
	})

	It("creates no file for a missing database", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "absent.db")
		_, _ = ReadStoreInfo(missing)
		_, err := os.Stat(missing)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("refuses a file that is not a database as config_invalid", func() {
		junk := filepath.Join(GinkgoT().TempDir(), "junk.db")
		if err := os.WriteFile(junk, []byte("this is not a sqlite database, just text padding"), 0o600); err != nil {
			panic(err)
		}
		_, err := ReadStoreInfo(junk)
		Expect(ErrorCode(err)).To(Equal(CodeConfigInvalid))
	})
})
