package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"satchel/internal/config"
)

func TestRunSetupConfiguresImageAndToolSwitch(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "nested", "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	outputDir := filepath.Join(root, "images")
	input := strings.Join([]string{
		"1", "primary", "1", "http://127.0.0.1:18081/v1", "test-secret", "gpt-image-test", outputDir,
		"2",
		"0",
	}, "\n") + "\n"
	in := inputFile(t, input)
	var out bytes.Buffer
	if err := RunSetup(in, &out); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.GenerateImage {
		t.Error("tool switch was not changed")
	}
	if cfg.Image.Default.Provider != "primary" || cfg.Image.Default.Model != "gpt-image-test" {
		t.Fatalf("default route = %+v", cfg.Image.Default)
	}
	provider := cfg.Image.Providers["primary"]
	if provider.API != "openai" || provider.BaseURL != "http://127.0.0.1:18081/v1" || provider.APIKey != "test-secret" {
		t.Fatalf("provider = %+v", provider)
	}
	if cfg.Image.OutputDir != outputDir {
		t.Fatalf("outputDir = %q", cfg.Image.OutputDir)
	}
	if !strings.Contains(out.String(), "Restart MCP clients") {
		t.Error("setup did not explain switch restart semantics")
	}
}

func TestRunSetupAbortDoesNotSave(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("SATCHEL_CONFIG", configPath)
	in := inputFile(t, "1\n")
	var out bytes.Buffer
	if err := RunSetup(in, &out); err == nil {
		t.Fatal("setup succeeded despite exhausted input")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("aborted setup wrote config: %v", err)
	}
}

func TestRunSetupConfiguresWebAndModuleSwitch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SATCHEL_CONFIG", filepath.Join(root, "config.json"))
	input := strings.Join([]string{
		"3", "tavily,searxng",
		"tavily-secret", "",
		"", "http://127.0.0.1:8080",
		"http://user:pass@127.0.0.1:7890",
		"4", "0",
	}, "\n") + "\n"
	in := inputFile(t, input)
	var out bytes.Buffer
	if err := RunSetup(in, &out); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.WebSearch || cfg.Tools.WebFetch || !cfg.Tools.GenerateImage {
		t.Fatalf("module switches = %+v", cfg.Tools)
	}
	if strings.Join(cfg.Web.Providers, ",") != "tavily,searxng" {
		t.Fatalf("providers = %v", cfg.Web.Providers)
	}
	if cfg.Web.APIKeys["tavily"] != "tavily-secret" || cfg.Web.BaseURLs["searxng"] != "http://127.0.0.1:8080" {
		t.Fatalf("web config = %+v", cfg.Web)
	}
	if cfg.Web.Proxy != "http://user:pass@127.0.0.1:7890" {
		t.Fatalf("proxy = %q", cfg.Web.Proxy)
	}
}

func TestConfigureWebAcceptsAllNineProviders(t *testing.T) {
	for _, name := range []string{"exa-free", "bing", "searxng", "bocha", "tavily", "exa", "brave", "jina", "firecrawl"} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Web.Providers = nil
			lines := []string{name}
			if name != "exa-free" && name != "bing" {
				lines = append(lines, "test-key")
			}
			if name == "searxng" {
				lines = append(lines, "http://127.0.0.1:8080")
			} else {
				lines = append(lines, "")
			}
			lines = append(lines, "")
			in := inputFile(t, strings.Join(lines, "\n")+"\n")
			var out bytes.Buffer
			p := &setupPrompter{in: in, out: &out, reader: bufio.NewReader(in)}
			if err := p.configureWeb(cfg); err != nil {
				t.Fatal(err)
			}
			if len(cfg.Web.Providers) != 1 || cfg.Web.Providers[0] != name || cfg.Web.BaseURLs[name] == "" {
				t.Fatalf("configured web provider = %+v", cfg.Web)
			}
		})
	}
}

func TestConfigureWebDoesNotReplaceExplicitEmptyChain(t *testing.T) {
	cfg := config.Default()
	cfg.Web.Providers = []string{}
	var out bytes.Buffer
	p := &setupPrompter{in: inputFile(t, "\n"), out: &out}
	p.reader = bufio.NewReader(p.in)
	if err := p.configureWeb(cfg); err == nil {
		t.Fatal("explicit empty chain was silently replaced")
	}
	if strings.Contains(out.String(), "[tavily]") || strings.Contains(out.String(), "[exa-free]") {
		t.Fatalf("setup offered a hidden replacement chain: %s", out.String())
	}
}

func TestRunSetupNeverPrintsExistingProxyPassword(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	cfg := config.Default()
	cfg.Web.Providers = []string{"tavily"}
	cfg.Web.APIKeys["tavily"] = "keep-key"
	cfg.Web.BaseURLs["tavily"] = "https://api.tavily.com"
	cfg.Web.Proxy = "http://user:password@127.0.0.1:7890"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	in := inputFile(t, "3\n\n\n\n\n0\n")
	var out bytes.Buffer
	if err := RunSetup(in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "user:password") || strings.Contains(out.String(), "password") {
		t.Fatalf("setup printed proxy credentials: %s", out.String())
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Web.Proxy != cfg.Web.Proxy {
		t.Error("blank proxy input did not preserve the existing value")
	}
}

func TestRunSetupConfiguresVisionAndSwitch(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	input := strings.Join([]string{
		"7", "1", "http://127.0.0.1:18086/v1", "vision-secret", "vision-model", "8", "0",
	}, "\n") + "\n"
	in := inputFile(t, input)
	var out bytes.Buffer
	if err := RunSetup(in, &out); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.ImageAsk {
		t.Error("vision module was not disabled")
	}
	if cfg.Vision.API != "openai-completions" || cfg.Vision.BaseURL != "http://127.0.0.1:18086/v1" || cfg.Vision.APIKey != "vision-secret" || cfg.Vision.Model != "vision-model" {
		t.Fatalf("vision config = %+v", cfg.Vision)
	}
}

func TestRunSetupConfiguresVideo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SATCHEL_CONFIG", filepath.Join(root, "config.json"))
	outputDir := filepath.Join(root, "videos")
	input := "9\n2\nhttp://127.0.0.1:18088/api/v3\nvideo-secret\nvideo-model\n" + outputDir + "\n0\n"
	in := inputFile(t, input)
	var out bytes.Buffer
	if err := RunSetup(in, &out); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Video.API != "ark" || cfg.Video.APIKey != "video-secret" || cfg.Video.Model != "video-model" || cfg.Video.OutputDir != outputDir {
		t.Fatalf("video config = %+v", cfg.Video)
	}
}

func TestShowConfigRedactsSecrets(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	cfg := config.Default()
	cfg.Image.Providers["p"] = config.ImageProvider{API: "openai", APIKey: "do-not-print", Headers: map[string]string{"X-Key": "also-secret"}}
	var out bytes.Buffer
	if err := ShowConfig(&out, cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "do-not-print") || strings.Contains(out.String(), "also-secret") || !strings.Contains(out.String(), "<redacted>") {
		t.Fatalf("unsafe config output: %s", out.String())
	}
}

func TestChooseAPIAllImageProviders(t *testing.T) {
	for choice, want := range map[string]string{"1": "openai", "2": "gemini", "3": "dashscope", "4": "ark", "5": "openrouter"} {
		var out bytes.Buffer
		p := &setupPrompter{in: inputFile(t, choice+"\n"), out: &out}
		p.reader = bufio.NewReader(p.in)
		got, err := p.chooseAPI("")
		if err != nil || got != want {
			t.Errorf("chooseAPI(%q) = %q, %v; want %q", choice, got, err, want)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	for _, valid := range []string{"http://localhost:8080/v1", "https://example.test/v1beta"} {
		if err := validateBaseURL(valid); err != nil {
			t.Errorf("valid URL %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "/v1", "file:///tmp/x", "https://example.test/v1?key=x", "https://example.test/v1#x"} {
		if err := validateBaseURL(invalid); err == nil {
			t.Errorf("invalid URL %q was accepted", invalid)
		}
	}
}

func inputFile(t *testing.T, input string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	return in
}
