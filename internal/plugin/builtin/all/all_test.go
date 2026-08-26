package all_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	plugin "github.com/baphled/flowstate/internal/plugin"

	// Blank-import the barrel so its init-side-effect chain runs
	// before the specs assert against plugin.RegisteredBuiltins().
	_ "github.com/baphled/flowstate/internal/plugin/builtin/all"
)

func TestAll(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Plugin Builtin All Suite")
}

// internal/plugin/builtin/all is the canonical barrel package per
// [[ADR - App Composition Root Boundary]]: every built-in plugin
// blank-imported here must be discoverable via plugin.RegisteredBuiltins
// after the init-side-effect chain runs. The guard specs below close the
// drift gap where a plugin package gets added with an init() but the
// barrel forgets the blank-import (or vice versa) — a class of bug that
// previously surfaced only in production startup logs.
var _ = Describe("Plugin builtin all barrel", func() {
	It("registers event-logger via the canonical init-based contract", func() {
		registrations := plugin.RegisteredBuiltins()
		names := registrationNames(registrations)
		Expect(names).To(ContainElement("event-logger"),
			"event-logger must self-register via plugin.RegisterBuiltin from its init() — see internal/plugin/eventlogger/register.go")
	})

	It("registers the rate-limit-detector via the canonical init-based contract", func() {
		registrations := plugin.RegisteredBuiltins()
		names := registrationNames(registrations)
		Expect(names).To(ContainElement("rate-limit-detector"),
			"failover/ratelimit must self-register via plugin.RegisterBuiltin from its init() — see internal/plugin/failover/ratelimit_plugin.go")
	})

	It("provides a non-nil factory for every registered builtin", func() {
		// Anchors the contract that init-time registrations must carry
		// a callable Factory. A plugin that registers metadata without
		// a factory would silently disappear from LoadBuiltins; the
		// guard fails closed instead.
		for _, r := range plugin.RegisteredBuiltins() {
			Expect(r.Factory).NotTo(BeNil(), "registration %q must carry a non-nil Factory", r.Name)
		}
	})
})

func registrationNames(rs []plugin.Registration) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}
