// Package video implements Satchel's narrow video-generation protocols.
//
// Protocol mapping is selectively adapted from Magpie internal/gateway/video.go
// at commit 4534561f6bb8f8353a67801fbe864a6502517022. Magpie is Copyright
// (c) 2026 yetone, MIT licensed; see THIRD_PARTY_NOTICES.md.
package video

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxVideoBytes        = int64(512 << 20)
	maxReferenceImages   = 8
	maxReferenceBytes    = int64(20 << 20)
	requestTimeout       = 5 * time.Minute
	videoDownloadTimeout = 5 * time.Minute
	referenceTimeout     = 2 * time.Minute
	defaultPollInterval  = 3 * time.Second
)

// Provider is one configured video-generation route. BaseURL includes any
// gateway path; the selected API's default path is added when its path is empty.
type Provider struct {
	API        string
	BaseURL    string
	APIKey     string
	Model      string
	Headers    map[string]string
	HTTPClient *http.Client
}

// Request is one video generation. Seconds is zero for the model default, or
// 1–15. Image is the first frame; ReferenceImages supply subject references.
// Every image is an absolute local path, an HTTP(S) URL, or a base64 data URL.
type Request struct {
	Prompt          string
	Seconds         int
	Size            string
	Image           string
	ReferenceImages []string
}

// Result identifies the exclusively created MP4.
type Result struct {
	Path string
}

// Generate creates a video, polls it to a terminal state, and downloads its
// MP4. Task IDs and the outer MCP's 20-second response window deliberately do
// not belong to this synchronous core.
func Generate(ctx context.Context, provider Provider, request Request, outputPath string) (Result, error) {
	return generate(ctx, provider, request, outputPath, nil, defaultPollInterval)
}

func generate(ctx context.Context, provider Provider, request Request, outputPath string, downloader *videoDownloader, pollInterval time.Duration) (Result, error) {
	provider.API = strings.ToLower(strings.TrimSpace(provider.API))
	provider.Model = strings.TrimSpace(provider.Model)
	if provider.API != "openai-videos" && provider.API != "ark" {
		return Result{}, fmt.Errorf("unsupported video API %q", provider.API)
	}
	if provider.Model == "" {
		return Result{}, errors.New("video model is required")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return Result{}, errors.New("video prompt is required")
	}
	if request.Seconds < 0 || request.Seconds > 15 {
		return Result{}, errors.New("video seconds must be from 1 to 15, or zero for the model default")
	}
	if len(request.ReferenceImages) > maxReferenceImages {
		return Result{}, fmt.Errorf("at most %d reference images are allowed", maxReferenceImages)
	}
	request.Size = strings.TrimSpace(request.Size)
	if provider.API == "ark" {
		if _, _, err := arkSize(request.Size); err != nil {
			return Result{}, err
		}
	}
	base, err := normalizeBase(provider)
	if err != nil {
		return Result{}, err
	}
	outputPath = filepath.Clean(strings.TrimSpace(outputPath))
	if !filepath.IsAbs(outputPath) {
		return Result{}, errors.New("video outputPath must be an absolute path")
	}
	// Reserve the exact destination before any provider work. Keeping this file
	// open also prevents a close/reopen race from replacing another file.
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Result{}, fmt.Errorf("create video output: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = output.Close()
			_ = os.Remove(outputPath)
		}
	}()

	var first *referenceImage
	if strings.TrimSpace(request.Image) != "" {
		ref, err := loadReference(ctx, downloader, request.Image)
		if err != nil {
			return Result{}, fmt.Errorf("load first-frame image: %w", err)
		}
		first = &ref
	}
	refs := make([]referenceImage, 0, len(request.ReferenceImages))
	for _, value := range request.ReferenceImages {
		ref, err := loadReference(ctx, downloader, value)
		if err != nil {
			return Result{}, fmt.Errorf("load reference image: %w", err)
		}
		refs = append(refs, ref)
	}

	api := videoAPI{provider: provider, base: base}
	id, err := api.create(ctx, request, first, refs)
	if err != nil {
		return Result{}, err
	}
	finished := false
	defer func() {
		if finished || ctx.Err() == nil {
			return
		}
		// The caller has stopped waiting, but the paid remote task may still be
		// running. Make a bounded best-effort cancellation without holding it.
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = api.cancel(cancelCtx, id)
	}()
	for {
		state, err := api.status(ctx, id)
		if err != nil {
			return Result{}, err
		}
		switch strings.ToLower(strings.TrimSpace(state.Status)) {
		case "completed", "succeeded", "done":
			rawURL := state.videoURL()
			switch {
			case rawURL != "":
				err = downloadVideo(ctx, downloader, rawURL, output)
			case provider.API == "openai-videos":
				err = api.content(ctx, id, output, downloader)
			default:
				err = errors.New("Ark completed the video but returned no video URL")
			}
			if err != nil {
				return Result{}, err
			}
			if err := output.Sync(); err != nil {
				return Result{}, fmt.Errorf("sync video output: %w", err)
			}
			if err := output.Close(); err != nil {
				return Result{}, fmt.Errorf("close video output: %w", err)
			}
			keep = true
			finished = true
			return Result{Path: outputPath}, nil
		case "failed", "expired", "cancelled", "canceled":
			return Result{}, state.failure(provider)
		case "", "queued", "pending", "in_progress", "running", "processing":
			// Poll again below. An omitted status is Magpie's queued default.
		default:
			return Result{}, fmt.Errorf("%s returned unknown video status %q", provider.API, cleanUpstreamText(provider, state.Status))
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return Result{}, ctx.Err()
		case <-timer.C:
		}
	}
}
