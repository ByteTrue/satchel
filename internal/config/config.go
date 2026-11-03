package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	CurrentVersion = 1

	ToolImageAsk      = "image_ask"
	ToolGenerateImage = "generate_image"
	ToolWebSearch     = "web_search"
	ToolWebFetch      = "web_fetch"

	redactedValue = "<redacted>"
)

var envReference = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)$`)

type Config struct {
	Version int          `json:"version"`
	Tools   Tools        `json:"tools"`
	Image   ImageConfig  `json:"image"`
	Vision  VisionConfig `json:"vision"`
	Video   VideoConfig  `json:"video"`
	Web     WebConfig    `json:"web"`
}

type Tools struct {
	ImageAsk      bool `json:"image_ask"`
	GenerateImage bool `json:"generate_image"`
	WebSearch     bool `json:"web_search"`
	WebFetch      bool `json:"web_fetch"`
}

func (t Tools) Enabled(name string) bool {
	switch name {
	case ToolImageAsk:
		return t.ImageAsk
	case ToolGenerateImage:
		return t.GenerateImage
	case ToolWebSearch:
		return t.WebSearch
	case ToolWebFetch:
		return t.WebFetch
	default:
		return false
	}
}

type ImageConfig struct {
	Default   ImageDefault             `json:"default"`
	OutputDir string                   `json:"outputDir,omitempty"`
	Providers map[string]ImageProvider `json:"providers"`
}

type ImageDefault struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type ImageProvider struct {
	API     string            `json:"api"`
	BaseURL string            `json:"baseUrl,omitempty"`
	APIKey  string            `json:"apiKey,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type VisionConfig struct {
	API     string            `json:"api"`
	BaseURL string            `json:"baseUrl,omitempty"`
	APIKey  string            `json:"apiKey,omitempty"`
	Model   string            `json:"model"`
	Headers map[string]string `json:"headers,omitempty"`
}

type ResolvedVision struct {
	API     string
	BaseURL string
	APIKey  string
	Model   string
	Headers map[string]string
}

type VideoConfig struct {
	API       string            `json:"api"`
	BaseURL   string            `json:"baseUrl,omitempty"`
	APIKey    string            `json:"apiKey,omitempty"`
	Model     string            `json:"model"`
	OutputDir string            `json:"outputDir,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type ResolvedVideo struct {
	API       string
	BaseURL   string
	APIKey    string
	Model     string
	OutputDir string
	Headers   map[string]string
}

type WebConfig struct {
	Providers []string          `json:"providers"`
	APIKeys   map[string]string `json:"apiKeys,omitempty"`
	BaseURLs  map[string]string `json:"baseUrls,omitempty"`
	Proxy     string            `json:"proxy,omitempty"`
}

type ResolvedWebProvider struct {
	Name    string
	APIKey  string
	BaseURL string
}

type ResolvedImage struct {
	Provider  string
	Model     string
	OutputDir string
	API       string
	BaseURL   string
	APIKey    string
	Headers   map[string]string
}

func Default() *Config {
	return &Config{
		Version: CurrentVersion,
		Tools: Tools{
			ImageAsk:      true,
			GenerateImage: true,
			WebSearch:     true,
			WebFetch:      true,
		},
		Image:  ImageConfig{Providers: make(map[string]ImageProvider)},
		Vision: VisionConfig{Headers: make(map[string]string)},
		Video:  VideoConfig{Headers: make(map[string]string)},
		Web: WebConfig{
			Providers: []string{"exa-free"},
			APIKeys:   make(map[string]string), BaseURLs: make(map[string]string),
		},
	}
}

func Path() (string, error) {
	if path := os.Getenv("SATCHEL_CONFIG"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(dir, "satchel", "config.json"), nil
}

func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	return cfg, nil
}

func Save(cfg *Config) error {
	if err := validate(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if _, decodeErr := decode(existing); decodeErr != nil {
			return fmt.Errorf("refusing to overwrite invalid config %s: %w", path, decodeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing config %s: %w", path, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	return nil
}

func ResolveImage(cfg *Config) (ResolvedImage, error) {
	if cfg == nil {
		return ResolvedImage{}, setupError("image generation is not configured")
	}
	selection := cfg.Image.Default
	provider, ok := cfg.Image.Providers[selection.Provider]
	if selection.Provider == "" || selection.Model == "" || !ok {
		return ResolvedImage{}, setupError("image generation is not configured")
	}
	if err := validateProvider(selection.Provider, provider); err != nil {
		return ResolvedImage{}, err
	}
	if cfg.Image.OutputDir != "" && !filepath.IsAbs(cfg.Image.OutputDir) {
		return ResolvedImage{}, fmt.Errorf("image outputDir must be absolute")
	}

	baseURL, err := resolveEnv(provider.BaseURL)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("resolve image provider %q baseUrl: %w", selection.Provider, err)
	}
	apiKey, err := resolveEnv(provider.APIKey)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("resolve image provider %q apiKey: %w", selection.Provider, err)
	}
	headers := make(map[string]string, len(provider.Headers))
	for name, value := range provider.Headers {
		resolved, err := resolveEnv(value)
		if err != nil {
			return ResolvedImage{}, fmt.Errorf("resolve image provider %q header %q: %w", selection.Provider, name, err)
		}
		headers[name] = resolved
	}
	return ResolvedImage{
		Provider:  selection.Provider,
		Model:     selection.Model,
		OutputDir: cfg.Image.OutputDir,
		API:       provider.API,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Headers:   headers,
	}, nil
}

func ResolveVision(cfg *Config) (ResolvedVision, error) {
	if cfg == nil || cfg.Vision.Model == "" || cfg.Vision.API == "" {
		return ResolvedVision{}, setupError("vision is not configured")
	}
	if !supportedVisionAPI(cfg.Vision.API) {
		return ResolvedVision{}, fmt.Errorf("unsupported vision api %q", cfg.Vision.API)
	}
	baseURL, err := resolveEnv(cfg.Vision.BaseURL)
	if err != nil {
		return ResolvedVision{}, fmt.Errorf("resolve vision baseUrl: %w", err)
	}
	apiKey, err := resolveEnv(cfg.Vision.APIKey)
	if err != nil {
		return ResolvedVision{}, fmt.Errorf("resolve vision apiKey: %w", err)
	}
	headers := make(map[string]string, len(cfg.Vision.Headers))
	for name, value := range cfg.Vision.Headers {
		resolved, err := resolveEnv(value)
		if err != nil {
			return ResolvedVision{}, fmt.Errorf("resolve vision header %q: %w", name, err)
		}
		headers[name] = resolved
	}
	return ResolvedVision{API: cfg.Vision.API, BaseURL: baseURL, APIKey: apiKey, Model: cfg.Vision.Model, Headers: headers}, nil
}

func ResolveVideo(cfg *Config) (ResolvedVideo, error) {
	if cfg == nil || cfg.Video.Model == "" || cfg.Video.API == "" {
		return ResolvedVideo{}, setupError("video is not configured")
	}
	if !supportedVideoAPI(cfg.Video.API) {
		return ResolvedVideo{}, fmt.Errorf("unsupported video api %q", cfg.Video.API)
	}
	baseURL, err := resolveEnv(cfg.Video.BaseURL)
	if err != nil {
		return ResolvedVideo{}, fmt.Errorf("resolve video baseUrl: %w", err)
	}
	apiKey, err := resolveEnv(cfg.Video.APIKey)
	if err != nil {
		return ResolvedVideo{}, fmt.Errorf("resolve video apiKey: %w", err)
	}
	headers := make(map[string]string, len(cfg.Video.Headers))
	for name, value := range cfg.Video.Headers {
		resolved, err := resolveEnv(value)
		if err != nil {
			return ResolvedVideo{}, fmt.Errorf("resolve video header %q: %w", name, err)
		}
		headers[name] = resolved
	}
	return ResolvedVideo{API: cfg.Video.API, BaseURL: baseURL, APIKey: apiKey, Model: cfg.Video.Model, OutputDir: cfg.Video.OutputDir, Headers: headers}, nil
}

func ResolveWebProviders(cfg *Config) ([]ResolvedWebProvider, error) {
	if cfg == nil || len(cfg.Web.Providers) == 0 {
		return nil, setupError("web search is not configured")
	}
	seen := make(map[string]bool)
	out := make([]ResolvedWebProvider, 0, len(cfg.Web.Providers))
	for _, name := range cfg.Web.Providers {
		name = strings.TrimSpace(name)
		if seen[name] {
			continue
		}
		seen[name] = true
		if !supportedWebProvider(name) {
			return nil, fmt.Errorf("unsupported web search provider %q", name)
		}
		apiKey, err := resolveEnv(cfg.Web.APIKeys[name])
		if err != nil {
			return nil, fmt.Errorf("resolve web provider %q apiKey: %w", name, err)
		}
		baseURL, err := resolveEnv(cfg.Web.BaseURLs[name])
		if err != nil {
			return nil, fmt.Errorf("resolve web provider %q baseUrl: %w", name, err)
		}
		out = append(out, ResolvedWebProvider{Name: name, APIKey: apiKey, BaseURL: baseURL})
	}
	return out, nil
}

func ResolveWebProxy(cfg *Config) (string, error) {
	if cfg == nil {
		return "", nil
	}
	proxy, err := resolveEnv(cfg.Web.Proxy)
	if err != nil {
		return "", fmt.Errorf("resolve web proxy: %w", err)
	}
	if proxy != "" {
		if err := validateHTTPURL(proxy, "web proxy", true); err != nil {
			return "", err
		}
	}
	return proxy, nil
}

func Redacted(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Vision.APIKey = cfg.Vision.APIKey
	if out.Vision.APIKey != "" {
		out.Vision.APIKey = redactedValue
	}
	if sensitiveConfiguredURL(out.Vision.BaseURL) {
		out.Vision.BaseURL = redactedValue
	}
	out.Vision.Headers = make(map[string]string, len(cfg.Vision.Headers))
	for name := range cfg.Vision.Headers {
		out.Vision.Headers[name] = redactedValue
	}
	out.Video.APIKey = cfg.Video.APIKey
	if out.Video.APIKey != "" {
		out.Video.APIKey = redactedValue
	}
	if sensitiveConfiguredURL(out.Video.BaseURL) {
		out.Video.BaseURL = redactedValue
	}
	out.Video.Headers = make(map[string]string, len(cfg.Video.Headers))
	for name := range cfg.Video.Headers {
		out.Video.Headers[name] = redactedValue
	}
	out.Web.APIKeys = make(map[string]string, len(cfg.Web.APIKeys))
	for name := range cfg.Web.APIKeys {
		out.Web.APIKeys[name] = redactedValue
	}
	out.Web.BaseURLs = make(map[string]string, len(cfg.Web.BaseURLs))
	for name, value := range cfg.Web.BaseURLs {
		if sensitiveConfiguredURL(value) {
			value = redactedValue
		}
		out.Web.BaseURLs[name] = value
	}
	out.Web.Proxy = cfg.Web.Proxy
	if cfg.Web.Proxy != "" && !envReference.MatchString(cfg.Web.Proxy) {
		if u, err := url.Parse(cfg.Web.Proxy); err == nil && u.User != nil {
			out.Web.Proxy = redactedValue
		}
	}
	out.Image.Providers = make(map[string]ImageProvider, len(cfg.Image.Providers))
	for name, provider := range cfg.Image.Providers {
		if provider.APIKey != "" {
			provider.APIKey = redactedValue
		}
		if sensitiveConfiguredURL(provider.BaseURL) {
			provider.BaseURL = redactedValue
		}
		provider.Headers = make(map[string]string, len(provider.Headers))
		for header := range cfg.Image.Providers[name].Headers {
			provider.Headers[header] = redactedValue
		}
		out.Image.Providers[name] = provider
	}
	return &out
}

func decode(data []byte) (*Config, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("config must be a JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, fmt.Errorf("decode config fields: %w", err)
	}
	for _, name := range []string{"tools", "image", "vision", "video", "web"} {
		if value, ok := fields[name]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("config field %q must be an object, not null", name)
		}
	}
	cfg := Default()
	cfg.Version = 0
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode config: trailing JSON data")
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validate(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d (want %d)", cfg.Version, CurrentVersion)
	}
	if cfg.Image.OutputDir != "" && !filepath.IsAbs(cfg.Image.OutputDir) {
		return errors.New("image outputDir must be an absolute path")
	}
	for name, provider := range cfg.Image.Providers {
		if err := validateProvider(name, provider); err != nil {
			return err
		}
	}
	if cfg.Vision.API != "" && !supportedVisionAPI(cfg.Vision.API) {
		return fmt.Errorf("unsupported vision api %q", cfg.Vision.API)
	}
	if cfg.Vision.BaseURL != "" && !envReference.MatchString(cfg.Vision.BaseURL) {
		if err := validateHTTPURL(cfg.Vision.BaseURL, "vision baseUrl", false); err != nil {
			return err
		}
	}
	if cfg.Video.OutputDir != "" && !filepath.IsAbs(cfg.Video.OutputDir) {
		return errors.New("video outputDir must be an absolute path")
	}
	if cfg.Video.API != "" && !supportedVideoAPI(cfg.Video.API) {
		return fmt.Errorf("unsupported video api %q", cfg.Video.API)
	}
	if cfg.Video.BaseURL != "" && !envReference.MatchString(cfg.Video.BaseURL) {
		if err := validateHTTPURL(cfg.Video.BaseURL, "video baseUrl", false); err != nil {
			return err
		}
	}
	seen := make(map[string]bool)
	for _, name := range cfg.Web.Providers {
		if !supportedWebProvider(name) {
			return fmt.Errorf("unsupported web search provider %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate web search provider %q", name)
		}
		seen[name] = true
	}
	for name := range cfg.Web.APIKeys {
		if !supportedWebProvider(name) {
			return fmt.Errorf("apiKey for unsupported web search provider %q", name)
		}
	}
	for name, value := range cfg.Web.BaseURLs {
		if !supportedWebProvider(name) {
			return fmt.Errorf("baseUrl for unsupported web search provider %q", name)
		}
		if value != "" && !envReference.MatchString(value) {
			if err := validateHTTPURL(value, "web provider "+name+" baseUrl", false); err != nil {
				return err
			}
		}
	}
	if cfg.Web.Proxy != "" && !envReference.MatchString(cfg.Web.Proxy) {
		if err := validateHTTPURL(cfg.Web.Proxy, "web proxy", true); err != nil {
			return err
		}
	}
	return nil
}

func validateProvider(name string, provider ImageProvider) error {
	switch provider.API {
	case "openai", "gemini", "dashscope", "ark", "openrouter":
	default:
		return fmt.Errorf("image provider %q has unsupported api %q", name, provider.API)
	}
	if provider.BaseURL != "" && !envReference.MatchString(provider.BaseURL) {
		if err := validateHTTPURL(provider.BaseURL, "image provider "+name+" baseUrl", false); err != nil {
			return err
		}
	}
	return nil
}

func sensitiveConfiguredURL(value string) bool {
	if value == "" || envReference.MatchString(value) {
		return false
	}
	u, err := url.Parse(value)
	return err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != ""
}

func supportedVisionAPI(api string) bool {
	switch api {
	case "openai-completions", "openai-responses", "anthropic-messages", "google-generative-ai":
		return true
	default:
		return false
	}
}

func supportedVideoAPI(api string) bool {
	return api == "openai-videos" || api == "ark"
}

func supportedWebProvider(name string) bool {
	switch name {
	case "exa-free", "bing", "searxng", "bocha", "tavily", "exa", "brave", "jina", "firecrawl":
		return true
	default:
		return false
	}
}

func validateHTTPURL(value, label string, allowUserInfo bool) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return fmt.Errorf("%s must be an absolute http:// or https:// URL", label)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%s must not contain query or fragment", label)
	}
	if !allowUserInfo && u.User != nil {
		return fmt.Errorf("%s must not contain credentials", label)
	}
	return nil
}

func resolveEnv(value string) (string, error) {
	match := envReference.FindStringSubmatch(value)
	if match == nil {
		return value, nil
	}
	resolved, ok := os.LookupEnv(match[1])
	if !ok {
		return "", fmt.Errorf("environment variable %s is not set", match[1])
	}
	return resolved, nil
}

func setupError(message string) error {
	return fmt.Errorf("%s; run 'satchel setup'", message)
}
