// Package vision asks one configured vision model about local images.
//
// Protocol shapes are selectively adapted from Magpie internal/gateway/chat.go,
// internal/gateway/responses.go, internal/gateway/anthropic.go,
// internal/gateway/gemini.go, internal/gateway/vision_mcp.go, and
// internal/provider/gemini_api.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022. HTTP safety helpers are adapted
// from Satchel internal/imagegen/imagegen.go, itself adapted from Magpie
// internal/gateway/draw.go at that baseline. Magpie is Copyright (c) 2026
// yetone, MIT licensed; see THIRD_PARTY_NOTICES.md.
package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxImageCount  = 4
	maxImageBytes  = 20 << 20
	requestTimeout = 55 * time.Second
)

const defaultQuestion = `Describe each image completely, in the order provided, so another AI model can answer from your description alone.
Transcribe all visible text exactly (OCR), preserving layout where possible, including code, terminal output, error messages, labels, file names, and numbers.
For screenshots, describe the app or page, layout, and UI state. For charts and tables, include axes, labels, and readable values. For diagrams, describe elements and connections. For photos and drawings, include relevant visual details.
Describe only what is visible; identify anything unreadable instead of guessing. No preamble.`

// Provider is one configured vision route. BaseURL may include a gateway path;
// the protocol's version path is added only when its path is empty.
type Provider struct {
	API        string
	BaseURL    string
	APIKey     string
	Model      string
	Headers    map[string]string
	HTTPClient *http.Client
}

// Answer is the model's visible text response.
type Answer struct {
	Text string
}

type redactedError struct {
	message string
	cause   error
}

func (err *redactedError) Error() string { return err.message }
func (err *redactedError) Unwrap() error { return err.cause }

type image struct {
	data     string
	mimeType string
}

// Ask sends 1–4 local images and one question using a non-streaming protocol.
// An empty or whitespace-only question requests a complete description and OCR.
func Ask(ctx context.Context, provider Provider, paths []string, question string) (Answer, error) {
	return ask(ctx, provider, question, func(ctx context.Context) ([]image, error) {
		return loadImages(ctx, paths)
	})
}

// Analyze sends one absolute local path, HTTP(S) URL, or base64 image data URL.
// Remote images are downloaded through the public-address guard, never by the
// provider, and all input forms are limited to 20 MiB before base64 encoding.
func Analyze(ctx context.Context, provider Provider, imageRef, prompt string) (Answer, error) {
	return analyzeWithDownloader(ctx, provider, imageRef, prompt, nil)
}

func analyzeWithDownloader(ctx context.Context, provider Provider, imageRef, prompt string, downloader *imageDownloader) (Answer, error) {
	return ask(ctx, provider, prompt, func(ctx context.Context) ([]image, error) {
		return loadImage(ctx, downloader, imageRef)
	})
}

func ask(ctx context.Context, provider Provider, question string, load func(context.Context) ([]image, error)) (answer Answer, err error) {
	defer func() {
		if err != nil {
			message := redactText(err.Error(), provider)
			if message != err.Error() {
				err = &redactedError{message: message, cause: err}
			}
		}
	}()

	provider.API = strings.ToLower(strings.TrimSpace(provider.API))
	provider.Model = strings.TrimSpace(provider.Model)
	switch provider.API {
	case "openai-completions", "openai-responses", "anthropic-messages", "google-generative-ai":
	default:
		return Answer{}, fmt.Errorf("unsupported vision API %q", provider.API)
	}
	if provider.Model == "" {
		return Answer{}, errors.New("vision model is required")
	}
	images, err := load(ctx)
	if err != nil {
		return Answer{}, err
	}
	if strings.TrimSpace(question) == "" {
		question = defaultQuestion
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := buildRequest(provider, images, question)
	if err != nil {
		return Answer{}, err
	}
	body, err := post(ctx, provider, request)
	if err != nil {
		return Answer{}, err
	}
	text, err := request.parse(body)
	if err != nil {
		return Answer{}, fmt.Errorf("%s response: %w", provider.API, err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Answer{}, fmt.Errorf("%s returned no text%s", provider.API, upstreamDetails(provider, body))
	}
	return Answer{Text: text}, nil
}

func loadImages(ctx context.Context, paths []string) ([]image, error) {
	if len(paths) == 0 {
		return nil, errors.New("at least one image path is required")
	}
	if len(paths) > maxImageCount {
		return nil, fmt.Errorf("at most %d images are allowed (got %d)", maxImageCount, len(paths))
	}
	images := make([]image, 0, len(paths))
	var total int64
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("image path %q must be absolute", path)
		}
		data, err := readImage(path, maxImageBytes-total)
		if err != nil {
			return nil, err
		}
		total += int64(len(data))
		mimeType := sniffImage(data)
		if mimeType == "" {
			return nil, fmt.Errorf("image %q must be PNG, JPEG, GIF, or WebP (checked by magic bytes)", path)
		}
		images = append(images, image{data: base64.StdEncoding.EncodeToString(data), mimeType: mimeType})
	}
	return images, nil
}

func readImage(path string, remaining int64) ([]byte, error) {
	// Stat before Open as well as after: opening a FIFO can block before its
	// non-regular type would otherwise be rejected.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat image %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("image %q is not a regular file", path)
	}
	if info.Size() > remaining {
		return nil, fmt.Errorf("images exceed the %d MiB total budget", maxImageBytes>>20)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image %q: %w", path, err)
	}
	info, err = file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	if err == nil && info.Size() > remaining {
		err = fmt.Errorf("images exceed the %d MiB total budget", maxImageBytes>>20)
	}
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(file, remaining+1))
	}
	closeErr := file.Close()
	if err != nil {
		return nil, fmt.Errorf("read image %q: %w", path, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close image %q: %w", path, closeErr)
	}
	if int64(len(data)) > remaining {
		return nil, fmt.Errorf("images exceed the %d MiB total budget", maxImageBytes>>20)
	}
	return data, nil
}

func sniffImage(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return "image/png"
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}
