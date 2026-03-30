package plugin

import (
	"fmt"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
)

// BusStarter is implemented by plugins that need to subscribe to the event
// bus after being instantiated. Start is called once during application
// startup after all builtin plugins have been loaded.
//
// Expected: Start is called once after plugin instantiation.
// Returns: error if startup fails.
// Side effects: may subscribe to the event bus; may allocate resources.
type BusStarter interface {
	// Start subscribes the plugin to the event bus.
	//
	// Expected: bus is non-nil.
	// Returns: error if startup fails.
	// Side effects: may subscribe to the event bus; may allocate resources.
	Start(bus *eventbus.EventBus) error
}

// StartBusPlugins calls Start on every BusStarter plugin in the registry.
//
// Expected: registry contains plugins; bus is non-nil.
// Returns: error if any BusStarter plugin's Start method fails.
// Side effects: invokes Start on each BusStarter plugin.
func StartBusPlugins(registry *Registry, bus *eventbus.EventBus) error {
	plugins := registry.List()
	for _, p := range plugins {
		starter, ok := p.(BusStarter)
		if !ok {
			continue
		}
		if err := starter.Start(bus); err != nil {
			return fmt.Errorf("starting plugin %s: %w", p.Name(), err)
		}
	}
	return nil
}
