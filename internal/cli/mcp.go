package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"satchel/internal/config"
	"satchel/internal/imagegen"
	"satchel/internal/mcpserver"
)

func RunMCP(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	server := mcpserver.New(mcpserver.Options{
		Modules:        currentModules(cfg),
		AnalyzeImage:   analyzeImage,
		Generate:       generateImage,
		GenerateVideo:  generateVideo,
		WebSearch:      webSearch,
		WebFetch:       webFetch,
		GenerationWait: 20 * time.Second,
	})
	return server.Serve(ctx, &mcp.StdioTransport{})
}

func generateImage(ctx context.Context, input mcpserver.GenerateInput) (mcpserver.GenerationResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	route, err := config.ResolveImage(cfg)
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	references, err := resolveMediaPaths(input.ReferenceImages)
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	images, err := imagegen.Generate(ctx, imagegen.Provider{
		API: route.API, BaseURL: route.BaseURL, APIKey: route.APIKey,
		Model: route.Model, Headers: route.Headers,
	}, imagegen.Request{
		Prompt: input.Prompt, Images: references, N: input.N, Size: input.Size,
		Quality: input.Quality, Background: input.Background,
	})
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	outputImages := make([]mcpserver.OutputImage, 0, len(images))
	var revised []string
	for _, image := range images {
		outputImages = append(outputImages, mcpserver.OutputImage{Data: image.Data, MIMEType: image.MIMEType})
		if text := strings.TrimSpace(image.RevisedPrompt); text != "" {
			revised = append(revised, text)
		}
	}
	paths, saveErr := mcpserver.SaveGenerationImages(route.OutputDir, input.Path, input.Prompt, outputImages)
	text := strings.Join(revised, "\n")
	if saveErr != nil && len(paths) > 0 {
		if text != "" {
			text += "\n\n"
		}
		text += "Some images could not be saved: " + saveErr.Error()
		saveErr = nil
	}
	if saveErr != nil {
		return mcpserver.GenerationResult{}, saveErr
	}
	if len(paths) == 0 {
		return mcpserver.GenerationResult{}, errors.New("provider returned no usable generated images")
	}
	return mcpserver.GenerationResult{Text: text, Files: paths}, nil
}

func resolveMediaPaths(references []string) ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(references))
	for i, reference := range references {
		reference = strings.TrimSpace(reference)
		switch {
		case strings.HasPrefix(reference, "~/"):
			reference = filepath.Join(home, reference[2:])
		case reference != "" && !filepath.IsAbs(reference) && !strings.HasPrefix(reference, "http://") && !strings.HasPrefix(reference, "https://") && !strings.HasPrefix(reference, "data:"):
			reference = filepath.Join(cwd, reference)
		}
		out[i] = reference
	}
	return out, nil
}
