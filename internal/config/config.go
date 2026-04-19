// Package config loads and saves the ostream CLI's on-disk config.
//
// Layout: ~/.config/ostream/config.json, mode 0600.
// Overridable via the OSTREAM_TOKEN / OSTREAM_URL env vars.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultRelayURL = "https://ostream.dev"

	envToken = "OSTREAM_TOKEN"
	envURL   = "OSTREAM_URL"
)

type Config struct {
	Token    string `json:"token,omitempty"`
	RelayURL string `json:"relay_url,omitempty"`
}

// Load reads the saved config from disk, applies env var overrides, and
// returns the effective settings. A missing config file is treated as
// empty (not an error) — the CLI works fine without a saved config when
// OSTREAM_TOKEN is set.
func Load() (*Config, error) {
	c := &Config{RelayURL: DefaultRelayURL}

	path, err := path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	// Env overrides.
	if v := os.Getenv(envToken); v != "" {
		c.Token = v
	}
	if v := os.Getenv(envURL); v != "" {
		c.RelayURL = v
	}
	if c.RelayURL == "" {
		c.RelayURL = DefaultRelayURL
	}
	return c, nil
}

// Save writes the config to disk with mode 0600. Creates the parent
// directory if necessary.
func Save(c *Config) error {
	path, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Clear removes the on-disk config.
func Clear() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Path returns the absolute path of the on-disk config.
func Path() (string, error) { return path() }

func path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(dir, "ostream", "config.json"), nil
}
