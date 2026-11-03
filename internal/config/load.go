package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileName is the FlowState configuration document name.
const FileName = "config.yaml"

// ErrInvalid marks configuration documents that fail to parse.
var ErrInvalid = fmt.Errorf("config_invalid")

// Path returns the user configuration path under
// $FLOWSTATE_CONFIG_DIR or os.UserConfigDir()/flowstate.
func Path() (string, error) {
	if dir := os.Getenv("FLOWSTATE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, FileName), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("%w: user config dir: %w", ErrInvalid, err)
	}
	return filepath.Join(base, "flowstate", FileName), nil
}

// Load reads the configuration document at path. A missing file
// yields the default (empty) configuration, not an error, so
// env-only setups keep working.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path) // #nosec G304 -- caller-supplied config path
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("%w: read %s: %w", ErrInvalid, path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%w: parse %s: %w", ErrInvalid, path, err)
	}
	if cfg.Providers == nil {
		cfg.Providers = make(map[string]*ProviderConfig)
	}
	return cfg, nil
}

// LoadDefault reads the configuration from the user config path.
func LoadDefault() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return Load(path)
}
