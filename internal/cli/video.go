package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
	"satchel/internal/video"
)

type videoGenerateFunc func(context.Context, video.Provider, video.Request, string) (video.Result, error)

func generateVideo(ctx context.Context, input mcpserver.VideoInput) (mcpserver.GenerationResult, error) {
	return generateVideoWithGenerator(ctx, input, video.Generate)
}

func generateVideoWithGenerator(ctx context.Context, input mcpserver.VideoInput, generateFn videoGenerateFunc) (mcpserver.GenerationResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	route, err := config.ResolveVideo(cfg)
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	references, err := resolveMediaPaths(input.ReferenceImages)
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	first, err := resolveMediaPaths([]string{input.Image})
	if err != nil {
		return mcpserver.GenerationResult{}, err
	}
	outputDir := route.OutputDir
	if outputDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return mcpserver.GenerationResult{}, err
		}
		outputDir = filepath.Join(cwd, "generated-videos")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		target, err := mcpserver.GenerationTarget(outputDir, input.Path, input.Prompt, ".mp4", 0, 1)
		if err != nil {
			return mcpserver.GenerationResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return mcpserver.GenerationResult{}, err
		}
		result, err := generateFn(ctx, video.Provider{
			API: route.API, BaseURL: route.BaseURL, APIKey: route.APIKey,
			Model: route.Model, Headers: route.Headers,
		}, video.Request{
			Prompt: input.Prompt, Seconds: input.Seconds, Size: input.Size,
			Image: first[0], ReferenceImages: references,
		}, target)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return mcpserver.GenerationResult{}, err
		}
		return mcpserver.GenerationResult{Files: []string{result.Path}}, nil
	}
}
