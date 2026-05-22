// Package config loads and saves the ostream CLI's on-disk config.
//
// Layout: $HOME/.ostream/config.json, mode 0600.
// Schema: a map of named profiles, plus a default_profile pointer. The
// older single-token schema is auto-migrated on load.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Dir returns the ostream CLI's base config directory (~/.ostream).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".ostream"), nil
}

const (
	DefaultRelayURL = "https://ostream.dev"
	DefaultName     = "default"

	envToken = "OSTREAM_TOKEN"
	envURL   = "OSTREAM_URL"
)

// Profile is one named (token, relay URL) pair.
type Profile struct {
	Token    string `json:"token,omitempty"`
	RelayURL string `json:"relay_url,omitempty"`
}

// Config is the in-memory shape of ~/.ostream/config.json. The legacy
// top-level Token/RelayURL fields are accepted on load (for backward
// compatibility with single-profile configs) and folded into Profiles
// on the next Save.
type Config struct {
	DefaultProfile string             `json:"default_profile,omitempty"`
	Profiles       map[string]Profile `json:"profiles,omitempty"`

	// Legacy fields — populated on load if the file is in the old schema.
	// Save never writes these (they're folded into Profiles).
	LegacyToken    string `json:"token,omitempty"`
	LegacyRelayURL string `json:"relay_url,omitempty"`
}

// Load reads the saved config from disk and migrates the legacy schema
// if needed. A missing file is treated as empty (not an error).
func Load() (*Config, error) {
	c := &Config{Profiles: map[string]Profile{}}

	p, err := path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	// Fold legacy fields into a default profile so the rest of the CLI
	// only has to think in terms of profiles.
	if c.LegacyToken != "" || c.LegacyRelayURL != "" {
		if _, ok := c.Profiles[DefaultName]; !ok {
			c.Profiles[DefaultName] = Profile{
				Token:    c.LegacyToken,
				RelayURL: c.LegacyRelayURL,
			}
		}
		c.LegacyToken = ""
		c.LegacyRelayURL = ""
	}
	if c.DefaultProfile == "" && len(c.Profiles) > 0 {
		// Prefer "default" if present; otherwise pick deterministically so
		// we don't surprise the user by switching between runs.
		if _, ok := c.Profiles[DefaultName]; ok {
			c.DefaultProfile = DefaultName
		} else {
			names := c.ProfileNames()
			c.DefaultProfile = names[0]
		}
	}
	return c, nil
}

// Save writes the config to disk with mode 0600. Creates the parent
// directory if necessary. Always writes the new (profile-aware) schema.
func Save(c *Config) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	// Make a copy and strip legacy fields before writing.
	out := *c
	out.LegacyToken = ""
	out.LegacyRelayURL = ""
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// Clear removes the on-disk config entirely.
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

// ProfileNames returns the profile names in sorted order.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Active resolves the active profile for this invocation:
//
//   - if `name` is non-empty, use that profile (error if missing);
//   - else if DefaultProfile is set and the profile exists, use it;
//   - else if a single profile exists, use it;
//   - else return an empty profile (caller should require a token before
//     making a network call).
//
// Env overrides (OSTREAM_TOKEN, OSTREAM_URL) are applied on top of the
// resolved profile. The returned name reflects what was picked.
func (c *Config) Active(name string) (Profile, string, error) {
	chosen := name
	if chosen == "" {
		chosen = c.DefaultProfile
	}
	var p Profile
	if chosen != "" {
		got, ok := c.Profiles[chosen]
		if !ok {
			return Profile{}, chosen, fmt.Errorf("profile %q not found", chosen)
		}
		p = got
	} else if len(c.Profiles) == 1 {
		for n, got := range c.Profiles {
			chosen = n
			p = got
		}
	}
	if v := os.Getenv(envToken); v != "" {
		p.Token = v
	}
	if v := os.Getenv(envURL); v != "" {
		p.RelayURL = v
	}
	if p.RelayURL == "" {
		p.RelayURL = DefaultRelayURL
	}
	return p, chosen, nil
}

// SetProfile creates or updates the named profile in c (in-memory; caller
// must Save to persist).
func (c *Config) SetProfile(name string, p Profile) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	c.Profiles[name] = p
}

// RemoveProfile deletes a profile. If it was the default, the default
// pointer is cleared and falls back to whatever resolution rule fires
// next time Active is called.
func (c *Config) RemoveProfile(name string) {
	delete(c.Profiles, name)
	if c.DefaultProfile == name {
		c.DefaultProfile = ""
	}
}

func path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}
