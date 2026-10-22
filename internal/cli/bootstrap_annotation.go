package cli

import "github.com/spf13/cobra"

// AnnotationBootstrap is the cobra.Command annotation key the root's
// PersistentPreRunE inspects to decide whether to invoke app.Bootstrap
// (the eight first-run XDG-mutating side effects: agent/skill/swarm/gate
// migration + seeding, mem0 wrapper materialisation, permissions.yaml
// write).
//
// Pre-this annotation, app.New ran Bootstrap unconditionally, which meant
// `flowstate help`, `flowstate version`, `flowstate <typo>` all
// materialised the seven seed directories AND permissions.yaml under
// whichever XDG_CONFIG_HOME resolved at the time. Now the cli root's
// PersistentPreRunE checks each command for this annotation set to "true"
// and gates the call accordingly. Commands that operate purely on already-
// loaded state (`models`, `session list`, `config show`, `--help`,
// `--version`, unknown subcommands) leave the annotation unset.
//
// Built-in cobra paths (`--help`, `--version`, unknown subcommand) never
// reach PersistentPreRunE, so they are inherently bootstrap-free without
// any annotation handling.
const AnnotationBootstrap = "flowstate.bootstrap"

// MarkNeedsBootstrap stamps cmd (and every nested subcommand reachable
// through cmd.Commands()) with AnnotationBootstrap=true so the cli root's
// PersistentPreRunE invokes app.Bootstrap before the leaf command runs.
//
// Stamping the parent + descendants matches Cobra's runtime: when the user
// invokes `flowstate auth user add`, the resolved command is `add` — so
// the annotation must live on the leaf, not on the `auth` parent.
// Pre-existing PersistentPreRunE inheritance handles propagation; this
// helper handles the annotation propagation.
//
// Expected:
//   - cmd is a non-nil cobra.Command. Nested subcommands attached after
//     this call must be marked separately (or via this helper) — Cobra
//     copies the parent's hooks but not arbitrary annotations.
//
// Side effects:
//   - Mutates cmd.Annotations and every descendant's Annotations to set
//     AnnotationBootstrap="true". Existing entries on the map are
//     preserved.
func MarkNeedsBootstrap(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[AnnotationBootstrap] = "true"
	for _, child := range cmd.Commands() {
		MarkNeedsBootstrap(child)
	}
}

// needsBootstrap reports whether cmd carries AnnotationBootstrap="true".
// Used by the cli root's PersistentPreRunE; broken out so test specs can
// assert annotation coverage without exercising the full Execute path.
//
// Expected:
//   - cmd may be nil; nil reports false (bootstrap is not run).
//
// Returns:
//   - true when cmd.Annotations[AnnotationBootstrap] == "true".
//   - false otherwise.
//
// Side effects:
//   - None.
func needsBootstrap(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	return cmd.Annotations[AnnotationBootstrap] == "true"
}
