// Smoke that loads the user's swarm directory + schema directory and
// validates each swarm's gate schema_refs resolve against the merged
// schema registry. Run from an integration worktree:
//
//	go run ./tools/smoke/swarm_validate
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/swarm"
)

func main() {
	home, err := os.UserHomeDir()
	must("home", err)
	swarmDir := filepath.Join(home, ".config", "flowstate", "swarms")
	schemaDir := filepath.Join(home, ".config", "flowstate", "schemas")
	agentsDir := filepath.Join(home, ".config", "flowstate", "agents")

	registry := agent.NewRegistry()
	must("discover agents", registry.Discover(agentsDir))
	fmt.Printf("agents loaded: %d\n", len(registry.List()))

	must("seed builtin schemas", swarm.SeedDefaultSchemas())
	fmt.Printf("builtin schemas registered: %d\n", len(swarm.RegisteredSchemaNames()))

	fmt.Printf("\nschema dir: %s\n", schemaDir)
	summary, err := swarm.LoadSchemasFromDir(schemaDir)
	must("load schemas", err)
	fmt.Printf("  user-registered: %d (failed: %d) names=%v\n", summary.Registered, summary.Failed, summary.Names)

	registered := map[string]bool{}
	for _, n := range swarm.RegisteredSchemaNames() {
		registered[n] = true
	}

	fmt.Printf("\nswarm dir: %s\n", swarmDir)
	manifests, err := swarm.LoadDir(swarmDir)
	must("load swarms", err)

	// Index manifests by ID so per-member resolution can fall back to
	// sub-swarm membership in O(1). Mirrors what the runtime does via
	// swarm.Resolve → KindSwarm; without it any roster entry naming a
	// sibling swarm (e.g. expert-consult inside planning-loop) would
	// be reported as MISSING even though execution accepts it.
	swarmIndex := make(map[string]*swarm.Manifest, len(manifests))
	for _, m := range manifests {
		swarmIndex[m.ID] = m
	}

	failures := 0
	for _, m := range manifests {
		fmt.Printf("\n  swarm: %s\n", m.ID)
		leadStatus := "OK"
		if _, ok := registry.GetByNameOrAlias(m.Lead); !ok {
			leadStatus = "MISSING"
			failures++
		}
		fmt.Printf("    lead: %-25s [%s]\n", m.Lead, leadStatus)
		for _, member := range m.Members {
			kind := resolveMemberKind(member, registry, swarmIndex)
			if kind == "MISSING" {
				failures++
			}
			fmt.Printf("    member: %-23s [%s]\n", member, kind)
		}
		fmt.Printf("    gates: %d\n", len(m.Harness.Gates))
		for _, g := range m.Harness.Gates {
			status := "OK"
			if g.SchemaRef != "" && !registered[g.SchemaRef] {
				status = "MISSING"
				failures++
			}
			fmt.Printf("      - %-40s when=%-12s target=%-15s schema_ref=%-30s [%s]\n",
				g.Name, g.When, g.Target, g.SchemaRef, status)
		}
	}

	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("swarms loaded: %d\n", len(manifests))
	fmt.Printf("schema-ref failures: %d\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func must(label string, err error) {
	if err != nil {
		fmt.Printf("FAIL %s: %v\n", label, err)
		os.Exit(1)
	}
}

// resolveMemberKind classifies a roster entry the same way the runtime
// resolver does: prefer agents, then fall back to registered sub-swarms.
// Returns "agent", "swarm", or "MISSING" for the per-member status column.
func resolveMemberKind(member string, registry *agent.Registry, swarmIndex map[string]*swarm.Manifest) string {
	if _, ok := registry.GetByNameOrAlias(member); ok {
		return "agent"
	}
	if _, ok := swarmIndex[member]; ok {
		return "swarm"
	}
	return "MISSING"
}
