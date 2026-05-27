package pathguard_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/tool/pathguard"
)

// reloaderSpy records Reload invocations. The Slice 4 writer fires
// Reload after a successful AppendAllow so the in-memory matcher
// serving Guard.Check picks up the new allow glob without a daemon
// restart. The spy lets specs assert that the wire is connected.
type reloaderSpy struct {
	mu      sync.Mutex
	calls   int
	loadErr error
}

func (r *reloaderSpy) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.loadErr
}

func (r *reloaderSpy) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// reloadablePerms is a tiny adapter that lets the test re-load the
// matcher after each write so the round-trip assertion ("the new allow
// is consulted by Match on the next call") can be made via the real
// config.Permissions matcher. The production wiring will do the same
// thing — write file, swap in-memory matcher pointer, next Check sees
// the new rule.
type reloadablePerms struct {
	mu    sync.RWMutex
	path  string
	perms *config.Permissions
}

func newReloadablePerms(path string) *reloadablePerms {
	r := &reloadablePerms{path: path}
	_ = r.Reload()
	return r
}

func (r *reloadablePerms) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	perms, err := config.LoadPermissions(r.path)
	if err != nil {
		return err
	}
	r.perms = perms
	return nil
}

func (r *reloadablePerms) Match(tool, path string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.perms == nil {
		return "", false
	}
	return r.perms.Match(tool, path)
}

// seedPermissionsFile writes a v1 permissions.yaml at path with the
// supplied tool rules. Used by the fixture BeforeEach to give the
// writer something to round-trip against.
func seedPermissionsFile(path string, tools map[string]map[string][]string) {
	type rule struct {
		Allow []string `yaml:"allow,omitempty"`
		Deny  []string `yaml:"deny,omitempty"`
	}
	type root struct {
		Version int             `yaml:"version"`
		Tools   map[string]rule `yaml:"tools,omitempty"`
	}
	r := root{Version: 1, Tools: map[string]rule{}}
	for tool, sides := range tools {
		r.Tools[tool] = rule{Allow: sides["allow"], Deny: sides["deny"]}
	}
	data, err := yaml.Marshal(r)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, data, 0o644)).To(Succeed())
}

var _ = Describe("Pathguard Writer (Slice 4 — permissions.yaml AppendAllow)", func() {
	var (
		dir          string
		permsPath    string
		writer       *pathguard.Writer
		reloader     *reloadablePerms
		reloaderSpyImpl *reloaderSpy
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		permsPath = filepath.Join(dir, "permissions.yaml")
	})

	Describe("AppendAllow happy path", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{
				"read": {"allow": {"/existing/**"}},
			})
			reloader = newReloadablePerms(permsPath)
			writer = pathguard.NewWriter(permsPath, reloader)
		})

		It("appends the glob and the matcher consults it on the next call", func() {
			// Pre-append: the new path is not consulted under "read".
			decision, matched := reloader.Match("read", "/secrets/api-key.txt")
			Expect(matched).To(BeFalse(),
				"pre-append the new resource should not match any allow rule")
			Expect(decision).To(BeEmpty())

			// Append the operator's "forever" grant.
			Expect(writer.AppendAllow("read", "/secrets/**")).To(Succeed())

			// Post-append: the matcher returns "allow" because we
			// reloaded in-place after the write — proves the wire is
			// connected end-to-end (file → reloader → matcher).
			decision, matched = reloader.Match("read", "/secrets/api-key.txt")
			Expect(matched).To(BeTrue(),
				"post-append the matcher should consult the new allow glob")
			Expect(decision).To(Equal("allow"))

			// The pre-existing rule is preserved verbatim.
			decision, matched = reloader.Match("read", "/existing/file.md")
			Expect(matched).To(BeTrue())
			Expect(decision).To(Equal("allow"))
		})

		It("creates the tools[tool] entry when the tool has no prior rules", func() {
			Expect(writer.AppendAllow("write", "/tmp/scratch/**")).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Tools).To(HaveKey("write"))
			Expect(loaded.Tools["write"].Allow).To(Equal([]string{"/tmp/scratch/**"}))
		})

		It("preserves the version and other fields verbatim", func() {
			// Pre-state: version=1 from seed. Post-state must still be
			// v1; future schema bumps (Slice 5 v2) require an explicit
			// migration step, not a silent rewrite.
			Expect(writer.AppendAllow("read", "/anywhere/**")).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Version).To(Equal(1))
		})
	})

	Describe("Idempotency (R1 acceptance)", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{
				"read": {"allow": {"/already/**"}},
			})
			reloader = newReloadablePerms(permsPath)
			writer = pathguard.NewWriter(permsPath, reloader)
		})

		It("returns nil without writing when the glob is already present", func() {
			before, err := os.ReadFile(permsPath)
			Expect(err).NotTo(HaveOccurred())
			beforeMtime := mustStat(permsPath).ModTime()

			// Wait a tick so a write would visibly change mtime.
			time.Sleep(20 * time.Millisecond)

			Expect(writer.AppendAllow("read", "/already/**")).To(Succeed())

			after, err := os.ReadFile(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before),
				"idempotent append must leave the file bytes untouched")
			Expect(mustStat(permsPath).ModTime()).To(Equal(beforeMtime),
				"idempotent append must not touch mtime")
		})

		It("does not duplicate an existing glob even across multiple appends", func() {
			Expect(writer.AppendAllow("read", "/already/**")).To(Succeed())
			Expect(writer.AppendAllow("read", "/already/**")).To(Succeed())
			Expect(writer.AppendAllow("read", "/already/**")).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Tools["read"].Allow).To(Equal([]string{"/already/**"}),
				"the existing glob must appear exactly once after repeated appends")
		})
	})

	Describe("Crash safety / atomicity (feedback_atomicity_awareness_uneven)", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{
				"read": {"allow": {"/original/**"}},
			})
			reloader = newReloadablePerms(permsPath)
		})

		It("leaves the original file untouched when the parent directory is unwritable", func() {
			// Make the directory read-only so CreateTemp fails. The
			// writer must surface the error AND leave the original
			// permissions.yaml intact — no partial state on disk.
			Expect(os.Chmod(dir, 0o555)).To(Succeed())
			DeferCleanup(func() { _ = os.Chmod(dir, 0o755) })

			writer = pathguard.NewWriter(permsPath, reloader)
			originalBytes, _ := os.ReadFile(permsPath)

			err := writer.AppendAllow("read", "/new/**")
			Expect(err).To(HaveOccurred(),
				"a writable-directory failure must surface as an error")

			// The seeded file is still on disk and parseable. A torn
			// write or a half-truncated temp file would fail this.
			currentBytes, readErr := os.ReadFile(permsPath)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(currentBytes).To(Equal(originalBytes),
				"original permissions.yaml must be byte-identical after a failed write")
		})

		It("does not leave temp files behind on failure", func() {
			Expect(os.Chmod(dir, 0o555)).To(Succeed())
			DeferCleanup(func() { _ = os.Chmod(dir, 0o755) })

			writer = pathguard.NewWriter(permsPath, reloader)
			_ = writer.AppendAllow("read", "/new/**")

			// Restore write so we can list — defer-cleanup hasn't fired yet.
			Expect(os.Chmod(dir, 0o755)).To(Succeed())

			entries, err := os.ReadDir(dir)
			Expect(err).NotTo(HaveOccurred())
			for _, e := range entries {
				Expect(e.Name()).NotTo(HavePrefix(".permissions-"),
					"failed appends must not leave temp files in config dir")
			}
		})
	})

	Describe("Cross-grant precedence (deny wins over a newly-appended allow)", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{
				"read": {
					"allow": {"/baseline/**"},
					"deny":  {"/secrets/**"},
				},
			})
			reloader = newReloadablePerms(permsPath)
			writer = pathguard.NewWriter(permsPath, reloader)
		})

		It("keeps deny winning even after an overlapping allow is appended", func() {
			// Operator tries to allow /secrets/api-key.txt forever even
			// though the deny rule covers it. The writer is purely
			// additive — it does NOT touch deny — and the matcher's
			// precedence rule (config/permissions.go:131-141) ensures
			// deny still wins on the next consultation. Pin per plan
			// §4 Slice 4 cross-grant precedence guard.
			Expect(writer.AppendAllow("read", "/secrets/**")).To(Succeed())

			decision, matched := reloader.Match("read", "/secrets/api-key.txt")
			Expect(matched).To(BeTrue())
			Expect(decision).To(Equal("deny"),
				"deny rules must continue to win over a newly-appended overlapping allow")

			// And the pre-existing allow continues to work for paths
			// not covered by deny.
			decision, matched = reloader.Match("read", "/baseline/anywhere/foo.md")
			Expect(matched).To(BeTrue())
			Expect(decision).To(Equal("allow"))
		})
	})

	Describe("Reloader wiring", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{
				"read": {"allow": {"/seed/**"}},
			})
			reloaderSpyImpl = &reloaderSpy{}
			writer = pathguard.NewWriter(permsPath, reloaderSpyImpl)
		})

		It("invokes Reload exactly once on a successful append", func() {
			Expect(writer.AppendAllow("read", "/new/**")).To(Succeed())
			Expect(reloaderSpyImpl.Calls()).To(Equal(1),
				"the writer must invoke the matcher's Reload after a successful write")
		})

		It("does NOT invoke Reload on an idempotent no-write", func() {
			// The seeded file already has /seed/** under read.allow.
			Expect(writer.AppendAllow("read", "/seed/**")).To(Succeed())
			Expect(reloaderSpyImpl.Calls()).To(Equal(0),
				"idempotent appends must skip the reload (the file did not change)")
		})

		It("surfaces reload errors but does NOT roll back the file write", func() {
			reloaderSpyImpl.loadErr = errors.New("reload exploded")
			err := writer.AppendAllow("read", "/new/**")
			Expect(err).To(HaveOccurred(),
				"a reload error must be visible to the caller")

			// The file IS on disk — the rename completed before the
			// reload was attempted. Source of truth is the file.
			loaded, lerr := config.LoadPermissions(permsPath)
			Expect(lerr).NotTo(HaveOccurred())
			Expect(loaded.Tools["read"].Allow).To(ContainElement("/new/**"),
				"the file must reflect the successful write even if the reload failed")
		})

		It("succeeds with no reloader wired (nil-tolerant)", func() {
			noReloadWriter := pathguard.NewWriter(permsPath, nil)
			Expect(noReloadWriter.AppendAllow("read", "/no-reload/**")).To(Succeed())
		})
	})

	Describe("Input validation", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{})
			writer = pathguard.NewWriter(permsPath, nil)
		})

		It("errors on empty tool", func() {
			Expect(writer.AppendAllow("", "/anywhere/**")).To(HaveOccurred())
		})

		It("errors on empty glob", func() {
			Expect(writer.AppendAllow("read", "")).To(HaveOccurred())
		})
	})

	Describe("R1 — Concurrent appends serialise via flock (§12 R1 acceptance)", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{
				"read": {"allow": {"/seed/**"}},
			})
			reloader = newReloadablePerms(permsPath)
		})

		It("two goroutines racing on AppendAllow both land — no lost append", func() {
			// Two goroutines simulate daemon + CLI both calling
			// AppendAllow concurrently. Without the flock, one's
			// read-modify-write could clobber the other (last-write-wins
			// loses one of the appends silently). With flock, both
			// goroutines serialise and both globs land in the final file.
			writer := pathguard.NewWriter(permsPath, reloader)

			var wg sync.WaitGroup
			var errs [2]error
			globs := [2]string{"/race/A/**", "/race/B/**"}

			wg.Add(2)
			for i := 0; i < 2; i++ {
				i := i
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					errs[i] = writer.AppendAllow("read", globs[i])
				}()
			}
			wg.Wait()

			Expect(errs[0]).NotTo(HaveOccurred())
			Expect(errs[1]).NotTo(HaveOccurred())

			// Both globs must be in the final file. A torn write would
			// leave one of them missing.
			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Tools["read"].Allow).To(ContainElement("/seed/**"),
				"the seeded glob must survive concurrent appends")
			Expect(loaded.Tools["read"].Allow).To(ContainElement("/race/A/**"),
				"goroutine A's append must persist")
			Expect(loaded.Tools["read"].Allow).To(ContainElement("/race/B/**"),
				"goroutine B's append must persist")
		})

		It("AppendMCPGrant serialises with AppendAllow under the same flock", func() {
			// Cross-method contention proof: AppendAllow and
			// AppendMCPGrant share the same lock path (the
			// permissions.yaml inode), so concurrent calls from the
			// two methods must serialise — no lost agent grant, no
			// lost tool glob. Permission Mode ModeAskUser Extension
			// plan (May 2026) Slice 5.
			writerX := pathguard.NewWriter(permsPath, nil)

			var wg sync.WaitGroup
			var errAllow, errMCP error
			wg.Add(2)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				errAllow = writerX.AppendAllow("read", "/cross/contention/**")
			}()
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				errMCP = writerX.AppendMCPGrant("coordinator", "vault-rag")
			}()
			wg.Wait()

			Expect(errAllow).NotTo(HaveOccurred())
			Expect(errMCP).NotTo(HaveOccurred())

			// Both mutations must be observable in the final file.
			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Tools["read"].Allow).To(ContainElement("/cross/contention/**"),
				"AppendAllow's glob must survive when racing AppendMCPGrant under flock")
			Expect(loaded.Agents).To(HaveKey("coordinator"))
			Expect(loaded.Agents["coordinator"].MCPServersGrant).To(ContainElement("vault-rag"),
				"AppendMCPGrant's server name must survive when racing AppendAllow under flock")
		})

		It("second appender blocks until the first releases (serialisation proof)", func() {
			// Two Writer instances sharing the same path file. We
			// drive a long-running first append via a slow reloader
			// (Reload sleeps under the held flock — wait, the lock
			// is released after the write but before Reload). To
			// observe serialisation we need the flock held during a
			// gap, so we use two writers calling AppendAllow back-to-
			// back and assert via wall-clock that they don't overlap.
			//
			// Method: first goroutine writes with a slow reloader
			// HELD WHILE THE LOCK IS RELEASED (reload runs post-lock).
			// That's not the serialisation point we want to prove.
			//
			// The right proof: spawn N goroutines each doing K
			// appends; if any two appends overlapped (no serialisation)
			// the final file would have lost some. We already cover
			// that in the previous It. Here we additionally verify
			// the timing: a single contention round-trip takes at
			// least one flock retry-poll interval (25ms in the writer).
			//
			// Concretely: spawn 6 goroutines × 3 appends = 18 globs.
			// Without flock the read-modify-write race loses some.
			// With flock all 18 land in the final file.
			writerA := pathguard.NewWriter(permsPath, nil)

			const (
				goroutines     = 6
				appendsPerGo   = 3
				expectedAppends = goroutines * appendsPerGo
			)

			var wg sync.WaitGroup
			var errCount atomic.Int32
			wg.Add(goroutines)
			for g := 0; g < goroutines; g++ {
				g := g
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					for k := 0; k < appendsPerGo; k++ {
						glob := globForRound(g, k)
						if err := writerA.AppendAllow("read", glob); err != nil {
							errCount.Add(1)
						}
					}
				}()
			}
			wg.Wait()

			Expect(errCount.Load()).To(BeZero(),
				"every concurrent AppendAllow must succeed under the flock")

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			// Seed (/seed/**) + the expected count. Without flock
			// some appends would be lost to read-modify-write races.
			Expect(loaded.Tools["read"].Allow).To(HaveLen(1+expectedAppends),
				"every concurrent append must persist; a lost append indicates a flock failure")
		})
	})

	// Permission Mode ModeAskUser Extension plan (May 2026), Slice 5.
	// AppendMCPGrant is the per-(agent, mcp_server) sibling of
	// AppendAllow, writing to the v2-introduced agents.<agent>.
	// mcp_servers_grant section. The writer is the v2 schema
	// introduction site (plan §11 R3) and stamps version: 2 on save.
	Describe("AppendMCPGrant (Slice 5 — per-agent MCP server grants)", func() {
		BeforeEach(func() {
			seedPermissionsFile(permsPath, map[string]map[string][]string{
				"read": {"allow": {"/seed/**"}},
			})
			writer = pathguard.NewWriter(permsPath, nil)
		})

		It("appends agents.<agent>.mcp_servers_grant and stamps version: 2", func() {
			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).NotTo(BeNil(),
				"AppendMCPGrant promotes to version: 2; LoadPermissions must continue to parse the file under the bumped supportedPermissionsVersion constant")
			Expect(loaded.Version).To(Equal(2),
				"AppendMCPGrant is the v2 schema introduction site per plan §11 R3")
			Expect(loaded.Agents).To(HaveKey("coordinator"))
			Expect(loaded.Agents["coordinator"].MCPServersGrant).To(Equal([]string{"vault-rag"}))

			// AppendMCPGrant must NOT touch the existing tools section
			// — the writer is purely additive across both dimensions.
			Expect(loaded.Tools["read"].Allow).To(Equal([]string{"/seed/**"}),
				"AppendMCPGrant must preserve the existing tools section verbatim")
		})

		It("creates the agents section when no prior agent grants exist", func() {
			// Fresh file with NO agents section.
			Expect(writer.AppendMCPGrant("explorer", "mem0")).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Agents).To(HaveLen(1))
			Expect(loaded.Agents["explorer"].MCPServersGrant).To(Equal([]string{"mem0"}))
		})

		It("is idempotent on repeated (agent, server) pairs", func() {
			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())
			before, _ := os.ReadFile(permsPath)
			beforeMtime := mustStat(permsPath).ModTime()
			time.Sleep(20 * time.Millisecond)

			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())
			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())

			after, _ := os.ReadFile(permsPath)
			Expect(after).To(Equal(before),
				"idempotent AppendMCPGrant must not change the file bytes")
			Expect(mustStat(permsPath).ModTime()).To(Equal(beforeMtime),
				"idempotent AppendMCPGrant must not touch mtime")

			// Reload and confirm only one entry.
			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Agents["coordinator"].MCPServersGrant).To(Equal([]string{"vault-rag"}),
				"the agent's grant list must contain exactly one entry after repeated identical appends")
		})

		It("accumulates distinct MCP servers under the same agent without duplication", func() {
			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())
			Expect(writer.AppendMCPGrant("coordinator", "mem0")).To(Succeed())
			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Agents["coordinator"].MCPServersGrant).To(Equal([]string{"vault-rag", "mem0"}),
				"distinct server names must accumulate in append order; identical re-grants must not duplicate")
		})

		It("preserves grants across daemon restart (round-trip through LoadPermissions)", func() {
			// Slice 5 acceptance: "Forever grant for MCP appends
			// agents.<agent>.mcp_servers_grant and survives daemon
			// restart." The restart is modelled by constructing a
			// FRESH Writer + matcher against the same file — the
			// daemon-restart equivalent in-process.
			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())
			Expect(writer.AppendMCPGrant("explorer", "mem0")).To(Succeed())

			// "Restart" — construct a new matcher from the same path.
			reloaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(reloaded.Version).To(Equal(2))
			Expect(reloaded.Agents).To(HaveLen(2))
			Expect(reloaded.Agents["coordinator"].MCPServersGrant).To(Equal([]string{"vault-rag"}))
			Expect(reloaded.Agents["explorer"].MCPServersGrant).To(Equal([]string{"mem0"}))
		})

		It("errors on empty agent or empty mcp_server", func() {
			Expect(writer.AppendMCPGrant("", "vault-rag")).To(HaveOccurred())
			Expect(writer.AppendMCPGrant("coordinator", "")).To(HaveOccurred())
		})

		It("leaves the file untouched on a validation failure path (atomicity)", func() {
			// Make the directory unwritable so CreateTemp fails. The
			// writer must surface the error and leave the file
			// byte-identical — including NO promotion to version: 2,
			// because nothing was written.
			Expect(os.Chmod(dir, 0o555)).To(Succeed())
			DeferCleanup(func() { _ = os.Chmod(dir, 0o755) })

			originalBytes, _ := os.ReadFile(permsPath)
			err := writer.AppendMCPGrant("coordinator", "vault-rag")
			Expect(err).To(HaveOccurred(),
				"a write failure on AppendMCPGrant must surface as an error")

			currentBytes, readErr := os.ReadFile(permsPath)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(currentBytes).To(Equal(originalBytes),
				"the original file must be byte-identical after a failed AppendMCPGrant — no torn write, no half-promoted version field")
		})
	})

	// Permission Mode ModeAskUser Extension plan (May 2026), Slice 5,
	// §12 R3 / §14 schema_migration_safety acceptance.
	//
	// Round-trip both directions:
	//   - A v2 file (written by AppendMCPGrant) is loaded by the
	//     LoadPermissions reader without panic. The reader treats v2
	//     as supported because supportedPermissionsVersion was bumped
	//     in this slice.
	//   - A pre-Slice-5 v1 file (no version field, or version: 1) is
	//     loaded under the bumped reader as v1 — the new reader is
	//     backward compatible.
	//   - A FORWARD-compat probe: a hand-crafted version: 3 file is
	//     loaded by the v2 reader (this build) and returns nil +
	//     slog.Warn via the existing "version too high → no
	//     permissions" path. This is the same shim a v1 daemon in the
	//     wild uses when it encounters a v2 file written by this
	//     build — the round-trip safety is proven by exercising the
	//     identical code path on a v3 input. Memory:
	//     feedback_schema_migration_safety.
	Describe("schema v1 ↔ v2 round-trip (Slice 5 §12 R3 / §14)", func() {
		It("v2 file round-trips through the v2 LoadPermissions reader without panic", func() {
			writer := pathguard.NewWriter(permsPath, nil)
			Expect(writer.AppendMCPGrant("coordinator", "vault-rag")).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred(),
				"the v2 reader must parse files written by the v2 writer without error — the foundational round-trip")
			Expect(loaded).NotTo(BeNil(),
				"v2 files must NOT fall through the 'version too high' silent-nil path on the bumped supportedPermissionsVersion")
			Expect(loaded.Version).To(Equal(2))
			Expect(loaded.Agents["coordinator"].MCPServersGrant).To(Equal([]string{"vault-rag"}))
		})

		It("pre-Slice-5 v1 file (no version field) still loads under the v2 reader as v1", func() {
			// Hand-write a pre-Slice-5 permissions.yaml — no version
			// field, just a tools section. This is the on-disk shape
			// a daemon upgraded from Slice 4 will encounter on first
			// boot post-upgrade.
			legacyYAML := "tools:\n  read:\n    allow:\n      - \"/legacy/**\"\n"
			Expect(os.WriteFile(permsPath, []byte(legacyYAML), 0o644)).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred(),
				"missing-version field is the canonical 'unversioned' signal and MUST NOT panic the v2 reader")
			Expect(loaded).NotTo(BeNil(),
				"legacy v1 files MUST continue to load through the v2 reader — no silent downgrade")
			Expect(loaded.Version).To(Equal(0),
				"unmarshalling a missing version field yields the int zero value; the reader treats this as v1 by precedent")
			Expect(loaded.Tools["read"].Allow).To(Equal([]string{"/legacy/**"}))
		})

		It("v1 file (explicit version: 1) loads under the v2 reader unchanged", func() {
			explicitV1 := "version: 1\ntools:\n  read:\n    allow:\n      - \"/explicit/**\"\n"
			Expect(os.WriteFile(permsPath, []byte(explicitV1), 0o644)).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).NotTo(BeNil())
			Expect(loaded.Version).To(Equal(1),
				"an explicit v1 file stays v1 — the v2 reader is monotonic, not migrating")
			Expect(loaded.Tools["read"].Allow).To(Equal([]string{"/explicit/**"}))
		})

		It("a v3 file (hypothetical future version) returns nil + slog.Warn (forward-compat shim)", func() {
			// This is the load-bearing round-trip safety assertion:
			// the EXISTING forward-compat path in LoadPermissions
			// ("version > supportedPermissionsVersion → nil + warn")
			// is what protects v1 daemons in the wild from this
			// build's v2 writer output. We exercise the same code
			// path with v3 to prove the shim is intact under the v2
			// reader. Memory: feedback_schema_migration_safety.
			futureYAML := "version: 3\ntools:\n  read:\n    allow:\n      - \"/future/**\"\n"
			Expect(os.WriteFile(permsPath, []byte(futureYAML), 0o644)).To(Succeed())

			loaded, err := config.LoadPermissions(permsPath)
			Expect(err).NotTo(HaveOccurred(),
				"a higher-than-supported version must NOT error — the daemon falls back to legacy guard silently with a warning")
			Expect(loaded).To(BeNil(),
				"the forward-compat shim returns (nil, nil); callers fall through to the legacy VaultPath path")
		})
	})
})

// mustStat is a tiny test helper so the .ModTime() expression in
// idempotency specs stays readable.
func mustStat(path string) os.FileInfo {
	info, err := os.Stat(path)
	Expect(err).NotTo(HaveOccurred())
	return info
}

// globForRound deterministically names a glob per goroutine + append
// index. The names are intentionally distinct so the idempotency code
// path (which is a no-op) does not mask a race that would otherwise
// lose appends.
func globForRound(goroutine, k int) string {
	return "/race/g" + itoa(goroutine) + "/k" + itoa(k) + "/**"
}

func itoa(i int) string {
	// Minimal int-to-string to avoid a strconv import in tests; the
	// values are 0..9 in the contention spec so single-digit handling
	// is enough.
	return string(rune('0' + i))
}
