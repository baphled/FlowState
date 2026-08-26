package plugin

// Plugin defines the interface for FlowState plugins.
//
// Expected:
//   - Init is called once before any hooks are invoked.
//   - Name returns the plugin's unique name.
//   - Version returns the plugin's version string.
type Plugin interface {
	// Init initialises the plugin before any hooks are invoked.
	// Expected: Returns an error if initialisation fails.
	Init() error
	// Name returns the plugin's unique name.
	// Expected: Returns a string identifying the plugin.
	Name() string
	// Version returns the plugin's version string.
	// Expected: Returns the plugin's version as a string.
	Version() string
}
