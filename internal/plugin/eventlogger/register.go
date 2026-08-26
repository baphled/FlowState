package eventlogger

import pluginpkg "github.com/baphled/flowstate/internal/plugin"

func init() {
	pluginpkg.RegisterBuiltin(pluginpkg.Registration{
		Name:             "event-logger",
		Order:            10,
		EnabledByDefault: true,
		Factory: func(d pluginpkg.Deps) (pluginpkg.Plugin, error) {
			return New(d.PluginsConfig.LogPath, d.PluginsConfig.LogSize), nil
		},
	})
}
