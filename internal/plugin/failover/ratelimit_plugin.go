package failover

import (
	"errors"

	pluginpkg "github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
)

// rateLimitPlugin wraps RateLimitDetector as a Plugin and BusStarter.
//
// Expected: instantiated via plugin factory with a valid HealthManager.
// Returns: implements plugin.Plugin and plugin.BusStarter.
// Side effects: none until Start is called.
type rateLimitPlugin struct {
	health   *HealthManager
	detector *RateLimitDetector
}

// Name returns the plugin name.
//
// Returns:
//   - "rate-limit-detector"
//
// Side effects:
//   - None.
func (p *rateLimitPlugin) Name() string { return "rate-limit-detector" }

// Version returns the plugin version.
//
// Returns:
//   - "1.0.0"
//
// Side effects:
//   - None.
func (p *rateLimitPlugin) Version() string { return "1.0.0" }

// Init is a no-op for rateLimitPlugin.
//
// Returns:
//   - nil always.
//
// Side effects:
//   - None.
func (p *rateLimitPlugin) Init() error { return nil }

// Start creates the RateLimitDetector and subscribes it to provider errors.
//
// Expected:
//   - bus is non-nil.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Subscribes to "provider.error" events on the bus.
func (p *rateLimitPlugin) Start(bus *eventbus.EventBus) error {
	p.detector = NewRateLimitDetector(bus, p.health)
	return nil
}

func init() {
	RegisterBuiltins()
}

// RegisterBuiltins registers the rate-limit detector builtin plugin.
//
// Side effects:
//   - Registers the "rate-limit-detector" factory in the global registry.
func RegisterBuiltins() {
	pluginpkg.RegisterBuiltin(pluginpkg.Registration{
		Name:             "rate-limit-detector",
		Order:            20,
		EnabledByDefault: true,
		Factory: func(d pluginpkg.Deps) (pluginpkg.Plugin, error) {
			health, ok := d.HealthManager.(*HealthManager)
			if !ok {
				return nil, errors.New("rate-limit-detector: HealthManager must be *failover.HealthManager")
			}
			return &rateLimitPlugin{health: health}, nil
		},
	})
}
