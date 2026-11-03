// Package imagegen implements Satchel's narrow image-generation protocols.
//
// Protocol and error-handling code is selectively adapted from Magpie
// internal/gateway/draw.go at commit 4534561f6bb8f8353a67801fbe864a6502517022.
// Magpie is Copyright (c) 2026 yetone, MIT licensed; see THIRD_PARTY_NOTICES.md.
package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxImages             = 8
	maxReferenceImages    = 8
	maxReferenceBytes     = 20 << 20
	maxResponseBytes      = 256 << 20
	maxErrorResponseBytes = 1 << 20
	requestTimeout        = 5 * time.Minute
)

// Provider is one configured image-generation route. BaseURL includes any
// gateway path; the selected API's default path is added when its path is empty.
type Provider struct {
	API        string
	BaseURL    string
	APIKey     string
	Model      string
	Headers    map[string]string
	HTTPClient *http.Client
}

// Request is one generation or edit. Images are absolute local file paths or
// public HTTP(S) URLs. N defaults to 1 and may be at most 8.
type Request struct {
	Prompt     string
	Images     []string
	N          int
	Size       string
	Quality    string
	Background string
}

// Image is an inline generated image.
type Image struct {
	Data          []byte
	MIMEType      string
	RevisedPrompt string
}

type referenceImage struct {
	data     []byte
	mimeType string
}

// Generate calls the configured image API and returns inline images. Remote
// references and URL-shaped results are downloaded without provider credentials.
func Generate(ctx context.Context, provider Provider, request Request) ([]Image, error) {
	return generate(ctx, provider, request, nil)
}

func generate(ctx context.Context, provider Provider, request Request, downloader *imageDownloader) (images []Image, err error) {
	defer func() {
		if err != nil {
			message := redactText(err.Error(), provider)
			if message != err.Error() {
				err = errors.New(message)
			}
		}
	}()
	provider.API = strings.ToLower(strings.TrimSpace(provider.API))
	provider.Model = strings.TrimSpace(provider.Model)
	switch provider.API {
	case "openai", "gemini", "dashscope", "ark", "openrouter":
	default:
		return nil, fmt.Errorf("unsupported image API %q", provider.API)
	}
	if provider.API != "openai" {
		if request.Quality != "" {
			return nil, fmt.Errorf("quality is unsupported for image API %q", provider.API)
		}
		if request.Background != "" {
			return nil, fmt.Errorf("background is unsupported for image API %q", provider.API)
		}
	}
	if provider.Model == "" {
		return nil, errors.New("image model is required")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, errors.New("image prompt is required")
	}
	if request.N <= 0 {
		request.N = 1
	}
	if request.N > maxImages {
		return nil, fmt.Errorf("n is at most %d", maxImages)
	}
	if len(request.Images) > maxReferenceImages {
		return nil, fmt.Errorf("at most %d reference images are allowed", maxReferenceImages)
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	refs, err := loadReferences(ctx, downloader, request.Images)
	if err != nil {
		return nil, err
	}
	switch provider.API {
	case "openai":
		return generateOpenAI(ctx, provider, request, refs, downloader)
	case "gemini":
		return generateGemini(ctx, provider, request, refs)
	case "dashscope":
		return generateDashscope(ctx, provider, request, refs, downloader)
	case "ark":
		return generateArk(ctx, provider, request, refs, downloader)
	default:
		return generateOpenRouter(ctx, provider, request, refs, downloader)
	}
}

func loadReferences(ctx context.Context, downloader *imageDownloader, paths []string) ([]referenceImage, error) {
	out := make([]referenceImage, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if hasHTTPScheme(path) {
			image, err := downloadImage(ctx, downloader, path, maxReferenceBytes)
			if err != nil {
				return nil, fmt.Errorf("download reference image: %w", err)
			}
			out = append(out, image)
			continue
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("reference image %q must be an absolute local path or HTTP(S) URL", path)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open reference image: %w", err)
		}
		data, err := readReferenceFile(f)
		closeErr := f.Close()
		if err != nil {
			return nil, fmt.Errorf("read reference image: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close reference image: %w", closeErr)
		}
		mimeType := sniffImage(data)
		if mimeType == "" {
			return nil, fmt.Errorf("reference image %q must be PNG, JPEG, GIF, or WebP", path)
		}
		out = append(out, referenceImage{data: data, mimeType: mimeType})
	}
	return out, nil
}

func readReferenceFile(f *os.File) ([]byte, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if st.Size() > maxReferenceBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", int64(maxReferenceBytes))
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReferenceBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxReferenceBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", int64(maxReferenceBytes))
	}
	return data, nil
}

func sniffImage(data []byte) string {
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mimeType
	default:
		return ""
	}
}

func inlineImage(encoded, declaredMIME string) (Image, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return Image{}, fmt.Errorf("image is not valid base64: %w", err)
	}
	if len(data) == 0 {
		return Image{}, errors.New("image base64 decodes to no bytes")
	}
	if int64(len(data)) > maxGeneratedImageBytes {
		return Image{}, fmt.Errorf("decoded image exceeds %d bytes", int64(maxGeneratedImageBytes))
	}
	mimeType := sniffImage(data)
	if mimeType == "" {
		declared, _, _ := mime.ParseMediaType(declaredMIME)
		switch strings.ToLower(declared) {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			mimeType = strings.ToLower(declared)
		default:
			mimeType = "image/png"
		}
	}
	return Image{Data: data, MIMEType: mimeType}, nil
}

func dataURLImage(value string) (Image, error) {
	// The caller has already checked the scheme case-insensitively.
	meta, data, ok := strings.Cut(value[len("data:"):], ",")
	if !ok || !strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return Image{}, errors.New("image data URL must contain base64 data")
	}
	declared := meta[:len(meta)-len(";base64")]
	return inlineImage(data, declared)
}

func normalizeBase(raw, defaultBase, defaultPath string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultBase
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return "", errors.New("provider baseURL must be an absolute http(s) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("provider baseURL must not contain credentials, query, or fragment")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = defaultPath
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func post(ctx context.Context, provider Provider, endpoint, contentType string, body []byte, authHeader, authValue string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New(redactText(err.Error(), provider))
	}
	req.Header.Set("Content-Type", contentType)
	if authValue != "" {
		req.Header.Set(authHeader, authValue)
	}
	for key, value := range provider.Headers {
		req.Header.Set(key, value)
	}
	client := provider.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	// Provider credentials must never be forwarded to a redirect target.
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	res, err := clone.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %s", provider.API, redactText(err.Error(), provider))
	}
	defer res.Body.Close()
	limit := int64(maxResponseBytes)
	if res.StatusCode >= http.StatusMultipleChoices {
		limit = maxErrorResponseBytes
	}
	responseBody, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %s", provider.API, redactText(err.Error(), provider))
	}
	if int64(len(responseBody)) > limit {
		return nil, fmt.Errorf("%s response exceeds %d bytes", provider.API, limit)
	}
	if res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s: %s%s", provider.API, res.Status, upstreamDetails(provider, responseBody))
	}
	return responseBody, nil
}

func vendorMessage(body []byte) string {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Error) > 0 {
		var object struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &object) == nil && object.Message != "" {
			return object.Message
		}
		var message string
		if json.Unmarshal(envelope.Error, &message) == nil {
			return message
		}
	}
	return string(body)
}

var upstreamURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

func upstreamDetails(provider Provider, body []byte) string {
	// Upstream free text may contain signed URLs. They are credentials, not
	// useful model-facing diagnostics, even when no configured key appears.
	message := redactText(vendorMessage(body), provider)
	message = upstreamURL.ReplaceAllString(message, "[URL]")
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return ""
	}
	runes := []rune(message)
	if len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return ": " + message
}

func redactText(message string, provider Provider) string {
	secrets := []string{provider.APIKey}
	for _, value := range provider.Headers {
		secrets = append(secrets, value)
	}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
		if escaped := url.QueryEscape(secret); escaped != secret {
			message = strings.ReplaceAll(message, escaped, "[REDACTED]")
		}
	}
	return message
}
