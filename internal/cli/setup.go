package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/term"

	"satchel/internal/config"
)

const (
	defaultOpenAIBase = "https://api.openai.com/v1"
	defaultGeminiBase = "https://generativelanguage.googleapis.com/v1beta"
)

type setupPrompter struct {
	in     *os.File
	out    io.Writer
	reader *bufio.Reader
}

func RunSetup(in *os.File, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	p := &setupPrompter{in: in, out: out, reader: bufio.NewReader(in)}
	for {
		fmt.Fprintln(out, "\nSatchel setup")
		fmt.Fprintln(out, "1. Configure image generation")
		fmt.Fprintf(out, "2. Image module (image, video, task results): %s\n", onOff(cfg.Tools.GenerateImage))
		fmt.Fprintln(out, "3. Configure web search and fetching")
		fmt.Fprintf(out, "4. Web Search module (search and fetch): %s\n", onOff(cfg.Tools.WebSearch && cfg.Tools.WebFetch))
		fmt.Fprintln(out, "6. Show redacted config")
		fmt.Fprintln(out, "7. Configure vision")
		fmt.Fprintf(out, "8. Vision module (analyze_image): %s\n", onOff(cfg.Tools.ImageAsk))
		fmt.Fprintln(out, "9. Configure video generation")
		fmt.Fprintln(out, "0. Save and exit")
		choice, err := p.prompt("Choose", "")
		if err != nil {
			return err
		}
		switch choice {
		case "0":
			if err := config.Save(cfg); err != nil {
				return err
			}
			path, _ := config.Path()
			fmt.Fprintf(out, "Saved %s\nRestart MCP clients for module changes to take effect.\n", path)
			return nil
		case "1":
			if err := p.configureImage(cfg); err != nil {
				return err
			}
		case "2":
			cfg.Tools.GenerateImage = !cfg.Tools.GenerateImage
		case "3":
			if err := p.configureWeb(cfg); err != nil {
				return err
			}
		case "4":
			enabled := !(cfg.Tools.WebSearch && cfg.Tools.WebFetch)
			cfg.Tools.WebSearch = enabled
			cfg.Tools.WebFetch = enabled
		case "5":
			fmt.Fprintln(out, "Web Search search and fetch are controlled together by choice 4.")
		case "6":
			if err := ShowConfig(out, cfg); err != nil {
				return err
			}
		case "7":
			if err := p.configureVision(cfg); err != nil {
				return err
			}
		case "8":
			cfg.Tools.ImageAsk = !cfg.Tools.ImageAsk
		case "9":
			if err := p.configureVideo(cfg); err != nil {
				return err
			}
		default:
			fmt.Fprintln(out, "Unknown choice; enter a number from 0 to 9.")
		}
	}
}

func ShowConfig(out io.Writer, cfg *config.Config) error {
	if cfg == nil {
		var err error
		cfg, err = config.Load()
		if err != nil {
			return err
		}
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Config: %s\n", path)
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(config.Redacted(cfg))
}

func (p *setupPrompter) configureImage(cfg *config.Config) error {
	selection := cfg.Image.Default
	providerName, err := p.prompt("Provider name", defaultString(selection.Provider, "default"))
	if err != nil {
		return err
	}
	if providerName == "" {
		return errors.New("provider name cannot be empty")
	}
	current := cfg.Image.Providers[providerName]
	api, err := p.chooseAPI(current.API)
	if err != nil {
		return err
	}
	baseDefault := current.BaseURL
	if baseDefault == "" {
		switch api {
		case "gemini":
			baseDefault = defaultGeminiBase
		case "dashscope":
			baseDefault = "https://dashscope.aliyuncs.com/api/v1"
		case "ark":
			baseDefault = "https://ark.cn-beijing.volces.com/api/v3"
		case "openrouter":
			baseDefault = "https://openrouter.ai/api/v1"
		default:
			baseDefault = defaultOpenAIBase
		}
	}
	baseURL, err := p.prompt("Base URL", baseDefault)
	if err != nil {
		return err
	}
	if err := validateBaseURL(baseURL); err != nil {
		return err
	}
	apiKey, err := p.secret("API key or $ENV_VAR (blank keeps current; no-key endpoints may stay blank)")
	if err != nil {
		return err
	}
	if apiKey == "" {
		apiKey = current.APIKey
	}
	modelDefault := selection.Model
	if selection.Provider != providerName {
		modelDefault = ""
	}
	model, err := p.prompt("Image model", modelDefault)
	if err != nil {
		return err
	}
	if model == "" {
		return errors.New("image model cannot be empty")
	}
	outputDefault := cfg.Image.OutputDir
	if outputDefault == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return homeErr
		}
		outputDefault = filepath.Join(home, "generated-images")
	}
	outputDir, err := p.prompt("Absolute output directory", outputDefault)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(outputDir) {
		return errors.New("output directory must be an absolute path")
	}
	if cfg.Image.Providers == nil {
		cfg.Image.Providers = make(map[string]config.ImageProvider)
	}
	cfg.Image.Providers[providerName] = config.ImageProvider{
		API: api, BaseURL: baseURL, APIKey: apiKey, Headers: current.Headers,
	}
	cfg.Image.Default = config.ImageDefault{Provider: providerName, Model: model}
	cfg.Image.OutputDir = outputDir
	return nil
}

func (p *setupPrompter) configureVideo(cfg *config.Config) error {
	defaultChoice := map[string]string{"openai-videos": "1", "ark": "2"}[cfg.Video.API]
	if defaultChoice == "" {
		defaultChoice = "1"
	}
	var api string
	for api == "" {
		choice, err := p.prompt("Video API: 1=OpenAI Videos, 2=Ark", defaultChoice)
		if err != nil {
			return err
		}
		api = map[string]string{"1": "openai-videos", "2": "ark"}[choice]
		if api == "" {
			fmt.Fprintln(p.out, "Choose 1 or 2.")
		}
	}
	baseDefault := cfg.Video.BaseURL
	if baseDefault == "" {
		if api == "ark" {
			baseDefault = "https://ark.cn-beijing.volces.com/api/v3"
		} else {
			baseDefault = defaultOpenAIBase
		}
	}
	baseURL, err := p.prompt("Video base URL", baseDefault)
	if err != nil {
		return err
	}
	if err := validateBaseURL(baseURL); err != nil {
		return err
	}
	apiKey, err := p.secret("Video API key or $ENV_VAR (blank keeps current)")
	if err != nil {
		return err
	}
	if apiKey == "" {
		apiKey = cfg.Video.APIKey
	}
	model, err := p.prompt("Video model", cfg.Video.Model)
	if err != nil {
		return err
	}
	if model == "" {
		return errors.New("video model cannot be empty")
	}
	outputDefault := cfg.Video.OutputDir
	if outputDefault == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return homeErr
		}
		outputDefault = filepath.Join(home, "generated-videos")
	}
	outputDir, err := p.prompt("Absolute video output directory", outputDefault)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(outputDir) {
		return errors.New("video output directory must be absolute")
	}
	cfg.Video.API = api
	cfg.Video.BaseURL = baseURL
	cfg.Video.APIKey = apiKey
	cfg.Video.Model = model
	cfg.Video.OutputDir = outputDir
	return nil
}

func (p *setupPrompter) configureVision(cfg *config.Config) error {
	defaultChoice := map[string]string{
		"openai-completions": "1", "openai-responses": "2",
		"anthropic-messages": "3", "google-generative-ai": "4",
	}[cfg.Vision.API]
	if defaultChoice == "" {
		defaultChoice = "1"
	}
	var api string
	for api == "" {
		choice, err := p.prompt("Vision API: 1=OpenAI Completions, 2=OpenAI Responses, 3=Anthropic, 4=Google", defaultChoice)
		if err != nil {
			return err
		}
		api = map[string]string{"1": "openai-completions", "2": "openai-responses", "3": "anthropic-messages", "4": "google-generative-ai"}[choice]
		if api == "" {
			fmt.Fprintln(p.out, "Choose 1, 2, 3, or 4.")
		}
	}
	baseDefault := cfg.Vision.BaseURL
	if baseDefault == "" {
		switch api {
		case "openai-completions", "openai-responses":
			baseDefault = defaultOpenAIBase
		case "anthropic-messages":
			baseDefault = "https://api.anthropic.com"
		case "google-generative-ai":
			baseDefault = defaultGeminiBase
		}
	}
	baseURL, err := p.prompt("Vision base URL", baseDefault)
	if err != nil {
		return err
	}
	if err := validateBaseURL(baseURL); err != nil {
		return err
	}
	apiKey, err := p.secret("Vision API key or $ENV_VAR (blank keeps current)")
	if err != nil {
		return err
	}
	if apiKey == "" {
		apiKey = cfg.Vision.APIKey
	}
	model, err := p.prompt("Vision model", cfg.Vision.Model)
	if err != nil {
		return err
	}
	if model == "" {
		return errors.New("vision model cannot be empty")
	}
	cfg.Vision.API = api
	cfg.Vision.BaseURL = baseURL
	cfg.Vision.APIKey = apiKey
	cfg.Vision.Model = model
	return nil
}

var wholeEnvReference = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*)$`)

var webProviderDefaults = map[string]string{
	"exa-free":  "https://mcp.exa.ai/mcp",
	"bing":      "https://www.bing.com",
	"bocha":     "https://api.bochaai.com",
	"tavily":    "https://api.tavily.com",
	"brave":     "https://api.search.brave.com",
	"exa":       "https://api.exa.ai",
	"jina":      "https://s.jina.ai",
	"firecrawl": "https://api.firecrawl.dev",
}

func (p *setupPrompter) configureWeb(cfg *config.Config) error {
	chainDefault := strings.Join(cfg.Web.Providers, ",")
	chain, err := p.prompt("Search providers (comma-separated: exa-free, bing, searxng, bocha, tavily, exa, brave, jina, firecrawl)", chainDefault)
	if err != nil {
		return err
	}
	var providers []string
	seen := make(map[string]bool)
	for _, name := range strings.Split(chain, ",") {
		name = strings.TrimSpace(name)
		_, known := webProviderDefaults[name]
		if name == "searxng" {
			known = true
		}
		if !known {
			return fmt.Errorf("unsupported web search provider %q", name)
		}
		if !seen[name] {
			providers = append(providers, name)
			seen[name] = true
		}
	}
	if len(providers) == 0 {
		return errors.New("at least one search provider is required")
	}
	if cfg.Web.APIKeys == nil {
		cfg.Web.APIKeys = make(map[string]string)
	}
	if cfg.Web.BaseURLs == nil {
		cfg.Web.BaseURLs = make(map[string]string)
	}
	for _, name := range providers {
		if name != "exa-free" && name != "bing" {
			key, err := p.secret("API key or $ENV_VAR for " + name + " (blank keeps current)")
			if err != nil {
				return err
			}
			if key == "" {
				key = cfg.Web.APIKeys[name]
			}
			cfg.Web.APIKeys[name] = key
		}

		baseDefault := cfg.Web.BaseURLs[name]
		if baseDefault == "" {
			baseDefault = webProviderDefaults[name]
		}
		base, err := p.prompt("Base URL for "+name+" (- uses provider default)", baseDefault)
		if err != nil {
			return err
		}
		if base == "-" {
			base = ""
		}
		if name == "searxng" && base == "" {
			return errors.New("searxng requires a base URL")
		}
		if base != "" {
			if err := validateBaseURL(base); err != nil {
				return err
			}
		}
		cfg.Web.BaseURLs[name] = base
	}
	proxy, err := p.prompt("HTTP(S) proxy for web fetching (blank keeps current, - disables)", "")
	if err != nil {
		return err
	}
	if proxy == "" {
		proxy = cfg.Web.Proxy
	} else if proxy == "-" {
		proxy = ""
	}
	if proxy != "" {
		if err := validateProxyURL(proxy); err != nil {
			return err
		}
	}
	cfg.Web.Providers = providers
	cfg.Web.Proxy = proxy
	return nil
}

func (p *setupPrompter) chooseAPI(current string) (string, error) {
	defaultChoice := map[string]string{"openai": "1", "gemini": "2", "dashscope": "3", "ark": "4", "openrouter": "5"}[current]
	if defaultChoice == "" {
		defaultChoice = "1"
	}
	for {
		choice, err := p.prompt("API: 1=OpenAI, 2=Gemini, 3=DashScope, 4=Ark, 5=OpenRouter", defaultChoice)
		if err != nil {
			return "", err
		}
		switch strings.ToLower(choice) {
		case "1", "openai":
			return "openai", nil
		case "2", "gemini":
			return "gemini", nil
		case "3", "dashscope":
			return "dashscope", nil
		case "4", "ark":
			return "ark", nil
		case "5", "openrouter":
			return "openrouter", nil
		default:
			fmt.Fprintln(p.out, "Choose a number from 1 to 5.")
		}
	}
}

func (p *setupPrompter) prompt(label, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", label, defaultValue)
	} else {
		fmt.Fprintf(p.out, "%s: ", label)
	}
	line, err := p.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", errors.New("setup input ended; no changes were saved")
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = defaultValue
	}
	return line, nil
}

func (p *setupPrompter) secret(label string) (string, error) {
	if !term.IsTerminal(int(p.in.Fd())) {
		return p.prompt(label, "")
	}
	fmt.Fprintf(p.out, "%s: ", label)
	secret, err := term.ReadPassword(int(p.in.Fd()))
	fmt.Fprintln(p.out)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret)), nil
}

func validateBaseURL(raw string) error {
	if wholeEnvReference.MatchString(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("base URL must be an absolute http:// or https:// URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("base URL cannot contain credentials, query, or fragment")
	}
	return nil
}

func validateProxyURL(raw string) error {
	if wholeEnvReference.MatchString(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("proxy must be an absolute http:// or https:// URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("proxy cannot contain query or fragment")
	}
	return nil
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
