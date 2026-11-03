package config

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPath(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", "")
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("user config directory unavailable: %v", err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(userDir, "satchel", "config.json")
	if path != want {
		t.Fatalf("Path() = %q, want %q", path, want)
	}

	override := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv("SATCHEL_CONFIG", override)
	path, err = Path()
	if err != nil {
		t.Fatal(err)
	}
	if path != override {
		t.Fatalf("Path() with override = %q, want %q", path, override)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "missing", "config.json"))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("Load() = %#v, want Default() %#v", cfg, Default())
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
	for _, name := range []string{ToolImageAsk, ToolGenerateImage, ToolWebSearch, ToolWebFetch} {
		if !cfg.Tools.Enabled(name) {
			t.Errorf("default tool %q is disabled", name)
		}
	}
	if cfg.Tools.Enabled("unknown") {
		t.Error("unknown tool is enabled")
	}
	if _, err := ResolveImage(cfg); err == nil || !strings.Contains(err.Error(), "satchel setup") {
		t.Fatalf("ResolveImage(Default()) error = %v, want setup guidance", err)
	}
}

func TestLoadAppliesToolDefaultsAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("SATCHEL_CONFIG", path)
	writeFile(t, path, `{"version":1,"tools":{"generate_image":false,"web_fetch":false}}`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tools.Enabled(ToolImageAsk) || !cfg.Tools.Enabled(ToolWebSearch) {
		t.Error("omitted tools did not retain their enabled defaults")
	}
	if cfg.Tools.Enabled(ToolGenerateImage) || cfg.Tools.Enabled(ToolWebFetch) {
		t.Error("explicitly disabled tools are enabled")
	}
}

func TestSaveLoadRoundTripPreservesLiteralSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "satchel", "config.json")
	t.Setenv("SATCHEL_CONFIG", path)
	cfg := Default()
	cfg.Tools.WebSearch = false
	cfg.Image.Default = ImageDefault{Provider: "primary", Model: "gpt-image-1"}
	cfg.Image.OutputDir = t.TempDir()
	cfg.Image.Providers["primary"] = ImageProvider{
		API:     "openai",
		BaseURL: "https://images.example.test/v1",
		APIKey:  "literal-secret",
		Headers: map[string]string{"Authorization": "Bearer literal-header"},
	}

	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", got, cfg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("literal-secret")) || !bytes.Contains(data, []byte("Bearer literal-header")) {
		t.Fatal("literal secrets were not preserved on disk")
	}
}

func TestLoadRejectsBadConfig(t *testing.T) {
	tests := map[string]string{
		"malformed":           `{`,
		"not an object":       `null`,
		"missing version":     `{}`,
		"null version":        `{"version":null}`,
		"trailing data":       `{}` + `{}`,
		"unknown field":       `{"version":1,"unexpected":true}`,
		"unsupported version": `{"version":2}`,
		"null web":            `{"version":1,"web":null}`,
		"null tools":          `{"version":1,"tools":null}`,
		"relative outputDir":  `{"version":1,"image":{"outputDir":"relative"}}`,
		"unsupported api":     `{"version":1,"image":{"providers":{"bad":{"api":"anthropic"}}}}`,
		"missing api":         `{"version":1,"image":{"providers":{"bad":{}}}}`,
		"image base userinfo": `{"version":1,"image":{"providers":{"bad":{"api":"openai","baseUrl":"https://user:secret@example.test/v1"}}}}`,
		"image base query":    `{"version":1,"image":{"providers":{"bad":{"api":"openai","baseUrl":"https://example.test/v1?api_key=secret"}}}}`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			t.Setenv("SATCHEL_CONFIG", path)
			writeFile(t, path, data)
			if _, err := Load(); err == nil {
				t.Fatal("Load() succeeded with invalid config")
			}
		})
	}
}

func TestLoadAllowsUnconfiguredImageAndSupportedAPIs(t *testing.T) {
	for _, api := range []string{"openai", "gemini", "dashscope", "ark", "openrouter"} {
		t.Run(api, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			t.Setenv("SATCHEL_CONFIG", path)
			writeFile(t, path, `{"version":1,"image":{"providers":{"p":{"api":"`+api+`"}}}}`)
			if _, err := Load(); err != nil {
				t.Fatalf("Load() rejected %s provider: %v", api, err)
			}
		})
	}
}

func TestSaveRefusesToOverwriteBadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("SATCHEL_CONFIG", path)
	bad := []byte(`{"version":1,"image":`)
	writeFile(t, path, string(bad))

	if err := Save(Default()); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("Save() error = %v, want refusal", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bad) {
		t.Fatalf("bad config was changed: got %q, want %q", got, bad)
	}
}

func TestResolveImageResolvesWholeEnvReferencesOnly(t *testing.T) {
	t.Setenv("SATCHEL_TEST_BASE_URL", "https://resolved.example.test")
	t.Setenv("SATCHEL_TEST_TOKEN", "resolved-token")
	cfg := Default()
	cfg.Image.Default = ImageDefault{Provider: "p", Model: "image-model"}
	cfg.Image.OutputDir = t.TempDir()
	cfg.Image.Providers["p"] = ImageProvider{
		API:     "gemini",
		BaseURL: "$SATCHEL_TEST_BASE_URL",
		APIKey:  "literal-secret",
		Headers: map[string]string{
			"Authorization": "$SATCHEL_TEST_TOKEN",
			"X-Literal":     "Bearer $SATCHEL_TEST_TOKEN",
		},
	}

	got, err := ResolveImage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "p" || got.Model != "image-model" || got.API != "gemini" || got.OutputDir != cfg.Image.OutputDir {
		t.Fatalf("resolved route mismatch: %#v", got)
	}
	if got.BaseURL != "https://resolved.example.test" {
		t.Errorf("BaseURL = %q", got.BaseURL)
	}
	if got.APIKey != "literal-secret" {
		t.Errorf("literal APIKey changed to %q", got.APIKey)
	}
	if got.Headers["Authorization"] != "resolved-token" {
		t.Errorf("resolved header = %q", got.Headers["Authorization"])
	}
	if got.Headers["X-Literal"] != "Bearer $SATCHEL_TEST_TOKEN" {
		t.Errorf("embedded env reference was expanded: %q", got.Headers["X-Literal"])
	}
	original := cfg.Image.Providers["p"]
	if original.BaseURL != "$SATCHEL_TEST_BASE_URL" || original.Headers["Authorization"] != "$SATCHEL_TEST_TOKEN" {
		t.Fatal("ResolveImage mutated the stored config")
	}
}

func TestResolveImageErrors(t *testing.T) {
	t.Run("missing environment variable", func(t *testing.T) {
		t.Setenv("SATCHEL_TEST_UNSET", "temporary")
		if err := os.Unsetenv("SATCHEL_TEST_UNSET"); err != nil {
			t.Fatal(err)
		}
		cfg := Default()
		cfg.Image.Default = ImageDefault{Provider: "p", Model: "m"}
		cfg.Image.Providers["p"] = ImageProvider{API: "openai", APIKey: "$SATCHEL_TEST_UNSET"}
		_, err := ResolveImage(cfg)
		if err == nil || !strings.Contains(err.Error(), "SATCHEL_TEST_UNSET") {
			t.Fatalf("ResolveImage() error = %v", err)
		}
	})

	for name, mutate := range map[string]func(*Config){
		"nil config":       func(*Config) {},
		"missing model":    func(c *Config) { c.Image.Default.Provider = "p"; c.Image.Providers["p"] = ImageProvider{API: "openai"} },
		"missing provider": func(c *Config) { c.Image.Default = ImageDefault{Provider: "p", Model: "m"} },
	} {
		t.Run(name, func(t *testing.T) {
			var cfg *Config
			if name != "nil config" {
				cfg = Default()
				mutate(cfg)
			}
			if _, err := ResolveImage(cfg); err == nil || !strings.Contains(err.Error(), "satchel setup") {
				t.Fatalf("ResolveImage() error = %v, want setup guidance", err)
			}
		})
	}
}

func TestRedactedDoesNotLeakOrMutate(t *testing.T) {
	cfg := Default()
	cfg.Image.Providers["p"] = ImageProvider{
		API:     "openai",
		BaseURL: "https://user:password@example.test/v1?api_key=url-secret",
		APIKey:  "top-secret",
		Headers: map[string]string{
			"Authorization": "Bearer secret-header",
			"X-Custom":      "custom-sensitive-value",
		},
	}
	redacted := Redacted(cfg)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"top-secret", "Bearer secret-header", "custom-sensitive-value", "user:password", "url-secret"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("redacted JSON contains %q: %s", secret, data)
		}
	}
	provider := redacted.Image.Providers["p"]
	if provider.APIKey != redactedValue {
		t.Errorf("redacted APIKey = %q", provider.APIKey)
	}
	if provider.BaseURL != redactedValue {
		t.Errorf("redacted BaseURL = %q", provider.BaseURL)
	}
	for name, value := range provider.Headers {
		if value != redactedValue {
			t.Errorf("redacted header %q = %q", name, value)
		}
	}
	original := cfg.Image.Providers["p"]
	if original.APIKey != "top-secret" || original.Headers["Authorization"] != "Bearer secret-header" {
		t.Fatal("Redacted mutated its input")
	}
	if Redacted(nil) != nil {
		t.Error("Redacted(nil) is not nil")
	}
}

func TestSavePermissionsAndAtomicReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions and open-file replacement semantics")
	}
	dir := filepath.Join(t.TempDir(), "satchel")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	t.Setenv("SATCHEL_CONFIG", path)
	old := Default()
	old.Image.OutputDir = filepath.Join(t.TempDir(), "old")
	if err := Save(old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oldFile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer oldFile.Close()

	newConfig := Default()
	newConfig.Image.OutputDir = filepath.Join(t.TempDir(), "new")
	if err := Save(newConfig); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config mode = %04o, want 0600", got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o755 {
		t.Errorf("Save changed an existing directory's permissions to %04o", got)
	}
	oldData, err := io.ReadAll(oldFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldData, before) {
		t.Fatal("an in-place write changed the already-open old config")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(after, before) {
		t.Fatal("config path still has old content")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("temporary files remain after Save: %v", entries)
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
