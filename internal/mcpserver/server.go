package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Module string

const (
	ModuleVision    Module = "vision"
	ModuleImage     Module = "image"
	ModuleWebSearch Module = "web-search"
)

var moduleTools = map[Module][]string{
	ModuleVision:    {"analyze_image"},
	ModuleImage:     {"generate_image", "generate_video", "generation_result"},
	ModuleWebSearch: {"web_search", "web_fetch"},
}

var (
	analyzeImageSchema     = json.RawMessage(`{"type":"object","properties":{"image":{"type":"string","description":"An absolute local image path, public HTTP(S) URL, or data URL"},"prompt":{"type":"string","description":"What to find out about the image; defaults to a full description and text transcription"}},"required":["image"]}`)
	generateImageSchema    = json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"size":{"type":"string"},"n":{"type":"integer","minimum":1,"maximum":4},"reference_images":{"type":"array","items":{"type":"string"}},"quality":{"type":"string"},"background":{"type":"string"},"path":{"type":"string"}},"required":["prompt"]}`)
	generateVideoSchema    = json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"seconds":{"type":"integer","minimum":1,"maximum":15},"size":{"type":"string"},"image":{"type":"string"},"reference_images":{"type":"array","items":{"type":"string"}},"path":{"type":"string"}},"required":["prompt"]}`)
	generationResultSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
	webSearchSchema        = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"What to search for"}},"required":["query"]}`)
	webFetchSchema         = json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"max_chars":{"type":"integer"},"offset":{"type":"integer"}},"required":["url"]}`)
)

type ModuleState struct {
	Vision    bool `json:"vision"`
	Image     bool `json:"image"`
	WebSearch bool `json:"web-search"`
}

func (s ModuleState) Enabled(module Module) bool {
	switch module {
	case ModuleVision:
		return s.Vision
	case ModuleImage:
		return s.Image
	case ModuleWebSearch:
		return s.WebSearch
	default:
		return false
	}
}

type AnalyzeImageInput struct {
	Image  string `json:"image" jsonschema:"An absolute local image path, public HTTP(S) URL, or data URL"`
	Prompt string `json:"prompt,omitempty" jsonschema:"What to find out about the image; defaults to a full description and text transcription"`
}

type AnalyzeImageResult struct {
	Text string
}

type AnalyzeImageFunc func(context.Context, AnalyzeImageInput) (AnalyzeImageResult, error)

type GenerateInput struct {
	Prompt          string   `json:"prompt" jsonschema:"What to draw, or how to change the reference images"`
	Size            string   `json:"size,omitempty" jsonschema:"WIDTHxHEIGHT or an aspect ratio supported by the configured model"`
	N               int      `json:"n,omitempty" jsonschema:"How many images, from 1 to 4 (default 1)"`
	ReferenceImages []string `json:"reference_images,omitempty" jsonschema:"Local paths or public HTTP(S) URLs of images to edit or combine"`
	Quality         string   `json:"quality,omitempty" jsonschema:"low, medium, high, or auto, for models that support it"`
	Background      string   `json:"background,omitempty" jsonschema:"transparent, opaque, or auto, for models that support it"`
	Path            string   `json:"path,omitempty" jsonschema:"Where to save: a file or folder, relative to the project or absolute; never overwritten"`
}

type VideoInput struct {
	Prompt          string   `json:"prompt" jsonschema:"What happens in the video: subject, motion, and camera"`
	Seconds         int      `json:"seconds,omitempty" jsonschema:"Length in whole seconds, from 1 to 15; default is the model's"`
	Size            string   `json:"size,omitempty" jsonschema:"An aspect ratio or WIDTHxHEIGHT supported by the configured model"`
	Image           string   `json:"image,omitempty" jsonschema:"An image to animate: a local path or public HTTP(S) URL"`
	ReferenceImages []string `json:"reference_images,omitempty" jsonschema:"Images the video subjects are drawn from"`
	Path            string   `json:"path,omitempty" jsonschema:"Where to save the MP4: a file or folder, relative to the project or absolute; never overwritten"`
}

type GenerationResult struct {
	Text  string
	Files []string
}

type GenerateFunc func(context.Context, GenerateInput) (GenerationResult, error)
type GenerateVideoFunc func(context.Context, VideoInput) (GenerationResult, error)

type WebSearchInput struct {
	Query string `json:"query" jsonschema:"The web search query"`
}

type WebSearchResult struct {
	Text       string
	Provider   string
	Unverified bool
}

type WebSearchFunc func(context.Context, WebSearchInput) (WebSearchResult, error)

type WebFetchInput struct {
	URL      string `json:"url" jsonschema:"The public http(s) page URL"`
	Offset   int    `json:"offset,omitempty" jsonschema:"Character offset for pagination (default 0)"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Maximum characters, at most 100000 (default 20000)"`
}

type WebFetchResult struct {
	Text       string
	URL        string
	Title      string
	NextOffset int
	TotalChars int
}

type WebFetchFunc func(context.Context, WebFetchInput) (WebFetchResult, error)

type Options struct {
	Modules        ModuleState
	AnalyzeImage   AnalyzeImageFunc
	Generate       GenerateFunc
	GenerateVideo  GenerateVideoFunc
	WebSearch      WebSearchFunc
	WebFetch       WebFetchFunc
	GenerationWait time.Duration
}

type Server struct {
	options Options
	mcp     *mcp.Server
	jobs    *generationManager
}

func New(options Options) *Server {
	if options.GenerationWait <= 0 {
		options.GenerationWait = 20 * time.Second
	}
	return &Server{options: options}
}

func (s *Server) Serve(ctx context.Context, transport mcp.Transport) error {
	jobs := newGenerationManager(ctx, s.options.GenerationWait)
	defer jobs.Close()
	server := mcp.NewServer(&mcp.Implementation{Name: "satchel", Version: "0.1.0-dev"}, &mcp.ServerOptions{HasTools: true})
	s.mcp = server
	s.jobs = jobs
	for _, module := range []Module{ModuleVision, ModuleImage, ModuleWebSearch} {
		if s.options.Modules.Enabled(module) {
			s.registerModule(module)
		}
	}
	return server.Run(ctx, transport)
}

func (s *Server) enabled(module Module) bool {
	return s.options.Modules.Enabled(module)
}

func (s *Server) registerModule(module Module) {
	switch module {
	case ModuleVision:
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "analyze_image",
			Description: "Look at an image and answer in text: the prompt, or a full description with its text transcribed.",
			InputSchema: analyzeImageSchema,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input AnalyzeImageInput) (*mcp.CallToolResult, analyzeImageToolOutput, error) {
			if !s.enabled(ModuleVision) {
				return nil, analyzeImageToolOutput{}, errors.New("vision module is disabled")
			}
			return runAnalyzeImage(ctx, s.options.AnalyzeImage, input)
		})
	case ModuleImage:
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name: "generate_image",
			Description: "Generate or edit images with the configured model. Saves files without overwriting. " +
				"If slow, returns an id for generation_result.",
			InputSchema: generateImageSchema,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input GenerateInput) (*mcp.CallToolResult, generationToolOutput, error) {
			if !s.enabled(ModuleImage) {
				return nil, generationToolOutput{}, errors.New("image module is disabled")
			}
			return s.startGeneration(ctx, input)
		})
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name: "generate_video",
			Description: "Generate a short video with the configured model, optionally from an image. " +
				"Saves an MP4 without overwriting; if slow, returns an id for generation_result.",
			InputSchema: generateVideoSchema,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input VideoInput) (*mcp.CallToolResult, generationToolOutput, error) {
			if !s.enabled(ModuleImage) {
				return nil, generationToolOutput{}, errors.New("image module is disabled")
			}
			return s.startVideo(ctx, input)
		})
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "generation_result",
			Description: "Get the result of a pending generate_image or generate_video task by its id.",
			InputSchema: generationResultSchema,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input generationResultInput) (*mcp.CallToolResult, generationToolOutput, error) {
			if !s.enabled(ModuleImage) {
				return nil, generationToolOutput{}, errors.New("image module is disabled")
			}
			return s.waitGeneration(ctx, input)
		})
	case ModuleWebSearch:
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "web_search",
			Description: "Search the web with the user's ordered provider chain and return titled sources with snippets.",
			InputSchema: webSearchSchema,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input WebSearchInput) (*mcp.CallToolResult, webSearchToolOutput, error) {
			if !s.enabled(ModuleWebSearch) {
				return nil, webSearchToolOutput{}, errors.New("web-search module is disabled")
			}
			return runWebSearch(ctx, s.options.WebSearch, input)
		})
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name:        "web_fetch",
			Description: "Fetch a public web page safely and return its text with links. Use offset and max_chars for long pages.",
			InputSchema: webFetchSchema,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input WebFetchInput) (*mcp.CallToolResult, webFetchToolOutput, error) {
			if !s.enabled(ModuleWebSearch) {
				return nil, webFetchToolOutput{}, errors.New("web-search module is disabled")
			}
			return runWebFetch(ctx, s.options.WebFetch, input)
		})
	}
}

type analyzeImageToolOutput struct {
	Text string `json:"text"`
}

type generationResultInput struct {
	ID string `json:"id" jsonschema:"The id returned by generate_image or generate_video"`
}

type generationToolOutput struct {
	Status string   `json:"status"`
	ID     string   `json:"id,omitempty"`
	Text   string   `json:"text,omitempty"`
	Files  []string `json:"files,omitempty"`
}

type webSearchToolOutput struct {
	Text       string `json:"text"`
	Provider   string `json:"provider,omitempty"`
	Unverified bool   `json:"unverified,omitempty"`
}

type webFetchToolOutput struct {
	Text       string `json:"text"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
	TotalChars int    `json:"total_chars,omitempty"`
}

func runAnalyzeImage(ctx context.Context, analyze AnalyzeImageFunc, input AnalyzeImageInput) (*mcp.CallToolResult, analyzeImageToolOutput, error) {
	if analyze == nil {
		return nil, analyzeImageToolOutput{}, errors.New("vision is not configured; run satchel setup")
	}
	input.Image = strings.TrimSpace(input.Image)
	if input.Image == "" {
		return nil, analyzeImageToolOutput{}, errors.New("analyze_image needs an image")
	}
	result, err := analyze(ctx, input)
	if err != nil {
		return nil, analyzeImageToolOutput{}, err
	}
	if strings.TrimSpace(result.Text) == "" {
		return nil, analyzeImageToolOutput{}, errors.New("vision model returned no text")
	}
	out := analyzeImageToolOutput{Text: result.Text}
	return textResult(result.Text), out, nil
}

func (s *Server) startGeneration(ctx context.Context, input GenerateInput) (*mcp.CallToolResult, generationToolOutput, error) {
	if s.options.Generate == nil {
		return nil, generationToolOutput{}, errors.New("image generation is not configured; run satchel setup")
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" {
		return nil, generationToolOutput{}, errors.New("generate_image needs a prompt")
	}
	if input.N == 0 {
		input.N = 1
	}
	if input.N < 1 || input.N > 4 {
		return nil, generationToolOutput{}, errors.New("generate_image n must be between 1 and 4")
	}
	if s.jobs == nil {
		return nil, generationToolOutput{}, errors.New("generation service is not running")
	}
	return startMediaGeneration(ctx, s.jobs, func(ctx context.Context) (GenerationResult, error) {
		return s.options.Generate(ctx, input)
	})
}

func (s *Server) startVideo(ctx context.Context, input VideoInput) (*mcp.CallToolResult, generationToolOutput, error) {
	if s.options.GenerateVideo == nil {
		return nil, generationToolOutput{}, errors.New("video generation is not configured; run satchel setup")
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" {
		return nil, generationToolOutput{}, errors.New("generate_video needs a prompt")
	}
	if input.Seconds < 0 || input.Seconds > 15 {
		return nil, generationToolOutput{}, errors.New("generate_video seconds must be between 1 and 15")
	}
	if s.jobs == nil {
		return nil, generationToolOutput{}, errors.New("generation service is not running")
	}
	return startMediaGeneration(ctx, s.jobs, func(ctx context.Context) (GenerationResult, error) {
		return s.options.GenerateVideo(ctx, input)
	})
}

func startMediaGeneration(ctx context.Context, jobs *generationManager, run generationRun) (*mcp.CallToolResult, generationToolOutput, error) {
	start, err := jobs.Start(ctx, run)
	if err != nil {
		return nil, generationToolOutput{}, err
	}
	if start.Pending {
		out := generationToolOutput{Status: "pending", ID: start.ID}
		return textResult("Generation is still running. Call generation_result with id " + strconv.Quote(start.ID) + "."), out, nil
	}
	out := generationToolOutput{Status: "completed", Text: start.Result.Text, Files: start.Result.Files}
	return textResult(completedGenerationText(start.Result)), out, nil
}

func (s *Server) waitGeneration(ctx context.Context, input generationResultInput) (*mcp.CallToolResult, generationToolOutput, error) {
	if strings.TrimSpace(input.ID) == "" {
		return nil, generationToolOutput{}, errors.New("generation_result needs an id")
	}
	if s.jobs == nil {
		return nil, generationToolOutput{}, errors.New("generation service is not running")
	}
	result, pending, err := s.jobs.Wait(ctx, input.ID)
	if err != nil {
		return nil, generationToolOutput{}, err
	}
	if pending {
		out := generationToolOutput{Status: "pending", ID: input.ID}
		return textResult("Generation is still running; call generation_result again with the same id."), out, nil
	}
	out := generationToolOutput{Status: "completed", ID: input.ID, Text: result.Text, Files: result.Files}
	return textResult(completedGenerationText(result)), out, nil
}

func runWebSearch(ctx context.Context, search WebSearchFunc, input WebSearchInput) (*mcp.CallToolResult, webSearchToolOutput, error) {
	if search == nil {
		return nil, webSearchToolOutput{}, errors.New("web search is not configured; run satchel setup")
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		return nil, webSearchToolOutput{}, errors.New("web_search needs a non-empty query")
	}
	result, err := search(ctx, input)
	if err != nil {
		return nil, webSearchToolOutput{}, err
	}
	out := webSearchToolOutput{Text: result.Text, Provider: result.Provider, Unverified: result.Unverified}
	return textResult(result.Text), out, nil
}

func runWebFetch(ctx context.Context, fetch WebFetchFunc, input WebFetchInput) (*mcp.CallToolResult, webFetchToolOutput, error) {
	if fetch == nil {
		return nil, webFetchToolOutput{}, errors.New("web fetch is not configured; run satchel setup")
	}
	input.URL = strings.TrimSpace(input.URL)
	if input.URL == "" {
		return nil, webFetchToolOutput{}, errors.New("web_fetch needs a URL")
	}
	if input.Offset < 0 {
		return nil, webFetchToolOutput{}, errors.New("web_fetch offset cannot be negative")
	}
	if input.MaxChars == 0 {
		input.MaxChars = 20_000
	}
	if input.MaxChars < 1 || input.MaxChars > 100_000 {
		return nil, webFetchToolOutput{}, errors.New("web_fetch max_chars must be between 1 and 100000")
	}
	result, err := fetch(ctx, input)
	if err != nil {
		return nil, webFetchToolOutput{}, err
	}
	out := webFetchToolOutput{Text: result.Text, URL: result.URL, Title: result.Title, NextOffset: result.NextOffset, TotalChars: result.TotalChars}
	return textResult(result.Text), out, nil
}

func completedGenerationText(result GenerationResult) string {
	var b strings.Builder
	if strings.TrimSpace(result.Text) != "" {
		b.WriteString(strings.TrimSpace(result.Text))
		b.WriteString("\n\n")
	}
	if len(result.Files) == 0 {
		b.WriteString("Generation completed without a saved file.")
		return b.String()
	}
	b.WriteString("Generated ")
	b.WriteString(strconv.Itoa(len(result.Files)))
	b.WriteString(" file(s):")
	for _, path := range result.Files {
		b.WriteString("\n- ")
		b.WriteString(path)
	}
	return b.String()
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
