package app

import (
	"log"
	"path/filepath"

	"github.com/baphled/flowstate/internal/config"
)

// Bootstrap runs the XDG-mutating first-run side effects FlowState relies on
// for a workable installation: legacy-layout migrations, embedded agent/skill/
// swarm/gate seeding, MCP memory-tools materialisation, and the
// tool-scoped-permissions default file.
//
// Every helper Bootstrap drives is idempotent (skip-on-existing) and
// failure-tolerant — individual write failures log a warning but never block
// startup. Bootstrap itself returns no error: it is intended to run on the
// success path of commands that need an initialised config dir
// (chat, run, serve, agent(s), skill, swarm, ...) and to be SKIPPED by
// commands that do not (--version, --help, unknown subcommand, vault index,
// config show, models, session list, ...).
//
// Pre-this refactor, the eight calls below lived inline at the top of
// app.New, which meant `flowstate help`, `flowstate version`, and
// `flowstate <typo>` all materialised the seven seed directories AND
// permissions.yaml under whichever XDG_CONFIG_HOME resolved at the time —
// even when the operator was experimenting against a tempdir export they
// later forgot. The extraction restores the single source of truth (this
// function) while letting `cmd/flowstate/main.go` construct the App without
// firing it; the cli root's PersistentPreRunE drives the gated call per
// command annotation (see internal/cli/bootstrap_annotation.go).
//
// Expected:
//   - cfg is a non-nil AppConfig with AgentDir, SkillDir, GatesDir, and
//     VaultPath populated as DefaultConfig would set them.
//
// Side effects:
//   - Copies legacy `~/.local/share/flowstate/{agents,skills}` content into
//     the configured XDG_CONFIG locations on the first post-flip startup.
//   - Materialises embedded agent manifests, skill bundles, swarm manifests,
//     and gate bundles into the configured directories (skip-on-existing).
//   - Materialises the bundled mem0 MCP wrapper under DefaultMemoryToolsDir().
//   - Writes permissions.yaml under config.Dir() substituting cfg.VaultPath
//     into the ${FLOWSTATE_VAULT_ROOT} placeholder.
//   - Each individual failure logs a warning via the standard logger but
//     does not block subsequent steps.
func Bootstrap(cfg *config.AppConfig) {
	if cfg == nil {
		return
	}

	// One-time XDG_DATA -> XDG_CONFIG migration: agent manifests and
	// skill bundles used to live in `~/.local/share/flowstate/{agents,skills}/`
	// but they are user-edited config and now belong in
	// `~/.config/flowstate/{agents,skills}/`. The helpers are no-ops when
	// the new dir already exists or the legacy dir is empty/missing, so
	// they are safe to call unconditionally on startup.
	migrateAgentsFromLegacyDataDir(cfg)
	migrateSkillsFromLegacyDataDir(cfg)

	if err := SeedAgentsDir(EmbeddedAgentsFS(), cfg.AgentDir); err != nil {
		log.Printf("warning: seeding agents to %q: %v", cfg.AgentDir, err)
	} else {
		log.Printf("info: agents seeded to %q", cfg.AgentDir)
	}

	// Seed the bundled skill manifests into cfg.SkillDir so the
	// engine's prompt-build path (loadSkills + LoadAlwaysActiveSkills)
	// finds the four always-active skills (pre-action, discipline,
	// skill-discovery, agent-discovery) and the rest of the baseline
	// on a fresh install. Without this seed step the loader walks an
	// empty dir and silently returns the empty slice — see
	// internal/skill/loader.go:44-46.
	if err := SeedSkillsDir(EmbeddedSkillsFS(), cfg.SkillDir); err != nil {
		log.Printf("warning: seeding skills to %q: %v", cfg.SkillDir, err)
	} else {
		log.Printf("info: skills seeded to %q", cfg.SkillDir)
	}

	swarmDir := resolveSwarmDir(cfg)
	if err := SeedSwarmsDir(EmbeddedSwarmsFS(), swarmDir); err != nil {
		log.Printf("warning: seeding swarms to %q: %v", swarmDir, err)
	} else {
		log.Printf("info: swarms seeded to %q", swarmDir)
	}

	// Seed the bundled gate bundles into cfg.GatesDir so the swarm
	// runner's `ext:*` kinds resolve to a registered runner on a fresh
	// install. Without this seed step the user has to manually
	// `cp -r examples/gates/<name> ~/.config/flowstate/gates/` before
	// any `ext:*` gate dispatch succeeds — see SeedGatesDir's godoc and
	// embed_gates.go for the bundled set.
	if cfg.GatesDir != "" {
		if err := SeedGatesDir(EmbeddedGatesFS(), cfg.GatesDir); err != nil {
			log.Printf("warning: seeding gates to %q: %v", cfg.GatesDir, err)
		} else {
			log.Printf("info: gates seeded to %q", cfg.GatesDir)
		}
	}

	// Auto-materialise the bundled mem0 MCP wrapper before the tool
	// pipeline assembles its MCP servers. DiscoverMCPServers probes
	// the install location first, so a freshly-materialised binary is
	// picked up on the same startup with no operator action. The hook
	// is idempotent (skip-on-existing) and failure-tolerant — a write
	// failure logs a warning but lets app init continue without
	// memory.
	MaterialiseMemoryToolsOnStartup(DefaultMemoryToolsDir())

	// Tool-Scoped Permissions plan (Slice C): materialise the embedded
	// default permissions.yaml under the XDG config dir on first run.
	// Idempotent (skip-on-existing) so operator customisation is never
	// overwritten; downgrades unwritable-dir and write-failure cases to
	// slog warnings so boot is never blocked. Behaviour-Pinned:
	// buildPathGuard at app.go:2651 reads <config.Dir()>/permissions.yaml
	// on every call, so this is the single ingestion point.
	if err := config.EnsurePermissionsFile(config.Dir(), cfg.VaultPath); err != nil {
		log.Printf("warning: bootstrapping permissions.yaml: %v", err)
	}
}

// expectedBootstrapPaths returns the filesystem paths Bootstrap would
// materialise or migrate into, in the order Bootstrap visits them. Reserved
// for the bootstrap test helpers; the cli layer uses
// internal/cli.AnnotationBootstrap + cli/root.go's PersistentPreRunE to
// decide when to invoke Bootstrap.
//
// Expected:
//   - cfg is a non-nil AppConfig.
//
// Returns:
//   - A slice of absolute paths (config dir, agents dir, skills dir, swarms
//     dir, gates dir, memory tools dir, permissions.yaml). May contain
//     duplicates when cfg defaults collapse two roots onto the same parent.
//
// Side effects:
//   - None.
func expectedBootstrapPaths(cfg *config.AppConfig) []string {
	if cfg == nil {
		return nil
	}
	return []string{
		cfg.AgentDir,
		cfg.SkillDir,
		resolveSwarmDir(cfg),
		cfg.GatesDir,
		DefaultMemoryToolsDir(),
		filepath.Join(config.Dir(), "permissions.yaml"),
	}
}
