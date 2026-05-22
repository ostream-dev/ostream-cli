package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withTempHome points HOME at a fresh temp dir for the duration of t.
// Necessary because Load/Save touch ~/.ostream/config.json and we don't
// want to clobber the developer's real config when running tests.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestLoad_MissingFileIsEmpty(t *testing.T) {
	withTempHome(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Profiles) != 0 {
		t.Errorf("expected no profiles, got %v", c.Profiles)
	}
}

func TestLoad_MigratesLegacySchema(t *testing.T) {
	home := withTempHome(t)
	legacy := `{"token": "os_legacy", "relay_url": "https://example.test"}`
	cfgPath := filepath.Join(home, ".ostream", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Profiles[DefaultName]; !ok || got.Token != "os_legacy" || got.RelayURL != "https://example.test" {
		t.Fatalf("legacy not migrated: %+v", c.Profiles)
	}
	if c.DefaultProfile != DefaultName {
		t.Errorf("default_profile = %q, want %q", c.DefaultProfile, DefaultName)
	}
}

func TestSave_StripsLegacyFields(t *testing.T) {
	home := withTempHome(t)
	c := &Config{
		DefaultProfile: "work",
		Profiles: map[string]Profile{
			"work": {Token: "os_work", RelayURL: "https://example.test"},
		},
		LegacyToken:    "should-not-be-written",
		LegacyRelayURL: "should-not-be-written",
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".ostream", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if _, ok := probe["token"]; ok {
		t.Errorf("legacy `token` field leaked to disk: %s", raw)
	}
	if _, ok := probe["relay_url"]; ok {
		t.Errorf("legacy `relay_url` field leaked to disk: %s", raw)
	}
}

func TestActive_PrefersExplicitName(t *testing.T) {
	c := &Config{
		DefaultProfile: "personal",
		Profiles: map[string]Profile{
			"personal": {Token: "p", RelayURL: "https://p.test"},
			"work":     {Token: "w", RelayURL: "https://w.test"},
		},
	}
	t.Setenv("OSTREAM_TOKEN", "")
	t.Setenv("OSTREAM_URL", "")
	got, name, err := c.Active("work")
	if err != nil {
		t.Fatal(err)
	}
	if name != "work" || got.Token != "w" {
		t.Errorf("got (%q, %+v), want (work, w)", name, got)
	}
}

func TestActive_FallsBackToDefault(t *testing.T) {
	c := &Config{
		DefaultProfile: "personal",
		Profiles: map[string]Profile{
			"personal": {Token: "p", RelayURL: "https://p.test"},
		},
	}
	t.Setenv("OSTREAM_TOKEN", "")
	t.Setenv("OSTREAM_URL", "")
	got, name, err := c.Active("")
	if err != nil || name != "personal" || got.Token != "p" {
		t.Fatalf("got (%q, %+v, %v)", name, got, err)
	}
}

func TestActive_SingleProfileAutoSelected(t *testing.T) {
	c := &Config{
		Profiles: map[string]Profile{
			"only": {Token: "o"},
		},
	}
	t.Setenv("OSTREAM_TOKEN", "")
	t.Setenv("OSTREAM_URL", "")
	got, name, err := c.Active("")
	if err != nil || name != "only" || got.Token != "o" {
		t.Fatalf("got (%q, %+v, %v)", name, got, err)
	}
}

func TestActive_EnvOverridesProfile(t *testing.T) {
	c := &Config{
		DefaultProfile: "p",
		Profiles:       map[string]Profile{"p": {Token: "p_tok", RelayURL: "https://p.test"}},
	}
	t.Setenv("OSTREAM_TOKEN", "env_tok")
	t.Setenv("OSTREAM_URL", "https://env.test")
	got, _, err := c.Active("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "env_tok" || got.RelayURL != "https://env.test" {
		t.Errorf("env didn't override: %+v", got)
	}
}

func TestActive_MissingProfileIsError(t *testing.T) {
	c := &Config{Profiles: map[string]Profile{"foo": {}}}
	if _, _, err := c.Active("nope"); err == nil {
		t.Errorf("expected error for unknown profile")
	}
}

func TestActive_DefaultsToRelayURLWhenUnset(t *testing.T) {
	c := &Config{DefaultProfile: "p", Profiles: map[string]Profile{"p": {Token: "t"}}}
	t.Setenv("OSTREAM_TOKEN", "")
	t.Setenv("OSTREAM_URL", "")
	got, _, _ := c.Active("")
	if got.RelayURL != DefaultRelayURL {
		t.Errorf("got %q, want %q", got.RelayURL, DefaultRelayURL)
	}
}

func TestRemoveProfile_ClearsDefault(t *testing.T) {
	c := &Config{
		DefaultProfile: "work",
		Profiles:       map[string]Profile{"work": {Token: "w"}, "home": {Token: "h"}},
	}
	c.RemoveProfile("work")
	if _, ok := c.Profiles["work"]; ok {
		t.Errorf("profile still present")
	}
	if c.DefaultProfile != "" {
		t.Errorf("default_profile not cleared: %q", c.DefaultProfile)
	}
}

func TestRoundTrip(t *testing.T) {
	withTempHome(t)
	c := &Config{
		DefaultProfile: "a",
		Profiles: map[string]Profile{
			"a": {Token: "os_a", RelayURL: "https://a.test"},
			"b": {Token: "os_b", RelayURL: "https://b.test"},
		},
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultProfile != "a" || loaded.Profiles["a"].Token != "os_a" || loaded.Profiles["b"].Token != "os_b" {
		t.Errorf("round trip lost data: %+v", loaded)
	}
}
