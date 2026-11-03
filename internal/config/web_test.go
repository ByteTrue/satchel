package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitEmptyWebProvidersRemainDisabledAfterSaveLoad(t *testing.T) {
	for _, providers := range [][]string{{}, nil} {
		t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
		cfg := Default()
		cfg.Web.Providers = providers
		if err := Save(cfg); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveWebProviders(loaded); err == nil {
			t.Fatalf("explicit empty providers became %v after save/load", loaded.Web.Providers)
		}
	}
}

func TestResolveWebProvidersAndProxy(t *testing.T) {
	t.Setenv("SATCHEL_TAVILY_KEY", "resolved-key")
	t.Setenv("SATCHEL_SEARX_URL", "https://search.example.test")
	t.Setenv("SATCHEL_PROXY", "http://user:pass@proxy.example.test:8080")
	cfg := Default()
	cfg.Web.Providers = []string{"tavily", "searxng", "tavily"}
	cfg.Web.APIKeys["tavily"] = "$SATCHEL_TAVILY_KEY"
	cfg.Web.BaseURLs["searxng"] = "$SATCHEL_SEARX_URL"
	cfg.Web.Proxy = "$SATCHEL_PROXY"
	providers, err := ResolveWebProviders(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].Name != "tavily" || providers[1].Name != "searxng" {
		t.Fatalf("providers = %+v", providers)
	}
	if providers[0].APIKey != "resolved-key" || providers[1].BaseURL != "https://search.example.test" {
		t.Fatalf("resolved providers = %+v", providers)
	}
	proxy, err := ResolveWebProxy(cfg)
	if err != nil || proxy != "http://user:pass@proxy.example.test:8080" {
		t.Fatalf("proxy = %q, %v", proxy, err)
	}
}

func TestAllNineWebProvidersLoadAndResolve(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	names := []string{"exa-free", "bing", "searxng", "bocha", "tavily", "exa", "brave", "jina", "firecrawl"}
	cfg := Default()
	cfg.Web.Providers = names
	cfg.Web.BaseURLs["searxng"] = "https://search.example.test"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveWebProviders(loaded)
	if err != nil || len(resolved) != len(names) {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	for i, name := range names {
		if resolved[i].Name != name {
			t.Errorf("provider %d = %q, want %q", i, resolved[i].Name, name)
		}
	}
}

func TestResolveWebDefaultAndExplicitEmpty(t *testing.T) {
	cfg := Default()
	providers, err := ResolveWebProviders(cfg)
	if err != nil || len(providers) != 1 || providers[0].Name != "exa-free" {
		t.Fatalf("default providers = %+v, %v", providers, err)
	}
	cfg.Web.Providers = []string{}
	if _, err := ResolveWebProviders(cfg); err == nil || !strings.Contains(err.Error(), "satchel setup") {
		t.Fatalf("explicit empty provider error = %v", err)
	}
}

func TestWebValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"provider":         func(c *Config) { c.Web.Providers = []string{"unknown"} },
		"duplicate":        func(c *Config) { c.Web.Providers = []string{"tavily", "tavily"} },
		"key name":         func(c *Config) { c.Web.APIKeys["unknown"] = "x" },
		"base URL":         func(c *Config) { c.Web.BaseURLs["searxng"] = "relative" },
		"base credentials": func(c *Config) { c.Web.BaseURLs["searxng"] = "https://key@example.test" },
		"proxy query":      func(c *Config) { c.Web.Proxy = "http://proxy.example.test/?x=1" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			cfg := Default()
			mutate(cfg)
			if err := Save(cfg); err == nil {
				t.Fatal("invalid web config was saved")
			}
		})
	}
}

func TestWebRedaction(t *testing.T) {
	cfg := Default()
	cfg.Web.APIKeys["tavily"] = "top-secret"
	cfg.Web.BaseURLs["searxng"] = "https://search.example.test"
	cfg.Web.Proxy = "http://user:password@proxy.example.test"
	redacted := Redacted(cfg)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"top-secret", "user:password"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("redacted config contains %q: %s", secret, data)
		}
	}
	if redacted.Web.BaseURLs["searxng"] != "https://search.example.test" {
		t.Error("non-secret base URL was unnecessarily hidden")
	}
	if cfg.Web.APIKeys["tavily"] != "top-secret" || cfg.Web.Proxy != "http://user:password@proxy.example.test" {
		t.Fatal("Redacted mutated its input")
	}
}
