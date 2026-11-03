package cli

import (
	"context"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
	"satchel/internal/vision"
)

func analyzeImage(ctx context.Context, input mcpserver.AnalyzeImageInput) (mcpserver.AnalyzeImageResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return mcpserver.AnalyzeImageResult{}, err
	}
	route, err := config.ResolveVision(cfg)
	if err != nil {
		return mcpserver.AnalyzeImageResult{}, err
	}
	answer, err := vision.Analyze(ctx, vision.Provider{
		API: route.API, BaseURL: route.BaseURL, APIKey: route.APIKey,
		Model: route.Model, Headers: route.Headers,
	}, input.Image, input.Prompt)
	if err != nil {
		return mcpserver.AnalyzeImageResult{}, err
	}
	return mcpserver.AnalyzeImageResult{Text: answer.Text}, nil
}
