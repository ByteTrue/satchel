// OpenAI-shaped and Ark request/response shapes are selectively adapted from
// Magpie internal/gateway/video.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022.
package video

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxResponseBytes      = int64(8 << 20)
	maxErrorResponseBytes = int64(1 << 20)
)

type videoAPI struct {
	provider Provider
	base     string
}

type videoState struct {
	Status string `json:"status"`
	Video  struct {
		URL string `json:"url"`
	} `json:"video"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (s videoState) videoURL() string {
	if value := strings.TrimSpace(s.Video.URL); value != "" {
		return value
	}
	return strings.TrimSpace(s.Content.VideoURL)
}

func (s videoState) failure(provider Provider) error {
	message := cleanUpstreamText(provider, s.Error.Message)
	if message == "" {
		message = "the video failed"
	}
	if code := cleanUpstreamText(provider, s.Error.Code); code != "" {
		return fmt.Errorf("video generation failed (%s): %s", code, message)
	}
	return fmt.Errorf("video generation failed: %s", message)
}

func normalizeBase(provider Provider) (string, error) {
	raw := strings.TrimSpace(provider.BaseURL)
	defaultBase, defaultPath := "", ""
	switch provider.API {
	case "openai-videos":
		defaultBase, defaultPath = "https://api.openai.com/v1", "/v1"
	case "ark":
		defaultBase, defaultPath = "https://ark.cn-beijing.volces.com/api/v3", "/api/v3"
	}
	if raw == "" {
		raw = defaultBase
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return "", errors.New("provider baseURL must be an absolute HTTP(S) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("provider baseURL must not contain credentials, query, or fragment")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = defaultPath
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (a videoAPI) taskURL(id string) string {
	if a.provider.API == "ark" {
		return a.base + "/contents/generations/tasks/" + url.PathEscape(id)
	}
	return a.base + "/videos/" + url.PathEscape(id)
}

func (a videoAPI) create(ctx context.Context, request Request, first *referenceImage, refs []referenceImage) (string, error) {
	var endpoint string
	var payload map[string]any
	if a.provider.API == "ark" {
		endpoint = a.base + "/contents/generations/tasks"
		content := []map[string]any{{"type": "text", "text": request.Prompt}}
		if first != nil {
			content = append(content, map[string]any{
				"type": "image_url", "image_url": map[string]string{"url": first.dataURL()}, "role": "first_frame",
			})
		}
		for _, ref := range refs {
			content = append(content, map[string]any{
				"type": "image_url", "image_url": map[string]string{"url": ref.dataURL()}, "role": "reference_image",
			})
		}
		payload = map[string]any{"model": a.provider.Model, "content": content}
		if request.Seconds > 0 {
			payload["duration"] = request.Seconds
		}
		ratio, resolution, err := arkSize(request.Size)
		if err != nil {
			return "", err
		}
		if ratio != "" {
			payload["ratio"] = ratio
		}
		if resolution != "" {
			payload["resolution"] = resolution
		}
	} else {
		endpoint = a.base + "/videos"
		payload = map[string]any{"model": a.provider.Model, "prompt": request.Prompt}
		if request.Seconds > 0 {
			payload["seconds"] = strconv.Itoa(request.Seconds)
		}
		if request.Size != "" {
			payload["size"] = request.Size
		}
		if first != nil {
			payload["input_reference"] = first.dataURL()
		}
		if len(refs) > 0 {
			values := make([]string, 0, len(refs))
			for _, ref := range refs {
				values = append(values, ref.dataURL())
			}
			payload["reference_images"] = values
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	responseBody, err := a.jsonRequest(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("%s create response is not valid JSON", a.provider.API)
	}
	response.ID = strings.TrimSpace(response.ID)
	if response.ID == "" {
		return "", fmt.Errorf("%s create response has no video id%s", a.provider.API, upstreamDetails(a.provider, responseBody))
	}
	return response.ID, nil
}

func (a videoAPI) status(ctx context.Context, id string) (videoState, error) {
	var state videoState
	body, err := a.jsonRequest(ctx, http.MethodGet, a.taskURL(id), nil)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return state, fmt.Errorf("%s status response is not valid video JSON", a.provider.API)
	}
	return state, nil
}

func (a videoAPI) cancel(ctx context.Context, id string) error {
	_, err := a.jsonRequest(ctx, http.MethodDelete, a.taskURL(id), nil)
	return err
}

func (a videoAPI) jsonRequest(ctx context.Context, method, endpoint string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, errors.New("invalid video provider endpoint")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	a.sign(req)
	client := providerClient(a.provider)
	res, err := client.Do(req)
	if err != nil {
		return nil, providerRequestError(ctx, a.provider, err)
	}
	defer res.Body.Close()
	limit := maxResponseBytes
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		limit = maxErrorResponseBytes
	}
	responseBody, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("read %s response failed", a.provider.API)
	}
	if int64(len(responseBody)) > limit {
		return nil, fmt.Errorf("%s response exceeds %d bytes", a.provider.API, limit)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s returned HTTP %d%s", a.provider.API, res.StatusCode, upstreamDetails(a.provider, responseBody))
	}
	return responseBody, nil
}

func (a videoAPI) content(ctx context.Context, id string, output io.Writer, downloader *videoDownloader) error {
	requestCtx, cancel := context.WithTimeout(ctx, videoDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, a.taskURL(id)+"/content", nil)
	if err != nil {
		return errors.New("invalid video content endpoint")
	}
	req.Header.Set("Accept", "video/mp4,application/octet-stream")
	a.sign(req)
	res, err := providerClient(a.provider).Do(req)
	if err != nil {
		return providerRequestError(requestCtx, a.provider, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= http.StatusMultipleChoices && res.StatusCode < http.StatusBadRequest {
		location, err := res.Location()
		if err != nil {
			return errors.New("video content redirect has no valid HTTP(S) location")
		}
		_ = res.Body.Close()
		// Redirected bytes are fetched as an untrusted URL with a clean request.
		// Provider credentials and custom headers never cross this boundary.
		return downloadVideo(ctx, downloader, location.String(), output)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorResponseBytes+1))
		return fmt.Errorf("video content returned HTTP %d%s", res.StatusCode, upstreamDetails(a.provider, body))
	}
	return copyVideo(requestCtx, res.Body, res.ContentLength, output, maxVideoBytes)
}

func (a videoAPI) sign(req *http.Request) {
	if a.provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.provider.APIKey)
	}
	for key, value := range a.provider.Headers {
		req.Header.Set(key, value)
	}
}

func providerClient(provider Provider) *http.Client {
	client := provider.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func providerRequestError(ctx context.Context, provider Provider, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%s request failed: %s", provider.API, cleanUpstreamText(provider, err.Error()))
}

func (r referenceImage) dataURL() string {
	return "data:" + r.mimeType + ";base64," + base64.StdEncoding.EncodeToString(r.data)
}

var arkRatios = []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"}

func arkSize(size string) (ratio, resolution string, err error) {
	if size == "" || strings.EqualFold(size, "auto") {
		return "", "", nil
	}
	ratioOnly := !strings.Contains(size, "x") && strings.Contains(size, ":")
	if ratioOnly {
		a, b, _ := strings.Cut(size, ":")
		x, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
		y, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
		if err1 != nil || err2 != nil || !positive(x) || !positive(y) {
			return "", "", fmt.Errorf("Ark size %q is not a valid aspect ratio", size)
		}
	}
	w, h, ok := strings.Cut(strings.ToLower(strings.Replace(size, ":", "x", 1)), "x")
	x, err1 := strconv.ParseFloat(w, 64)
	y, err2 := strconv.ParseFloat(h, 64)
	if !ok || err1 != nil || err2 != nil || !positive(x) || !positive(y) {
		return "", "", fmt.Errorf("Ark size %q must be an aspect ratio or WIDTHxHEIGHT", size)
	}
	best, bestDistance := "", math.Inf(1)
	for _, candidate := range arkRatios {
		a, b, _ := strings.Cut(candidate, ":")
		ra, _ := strconv.ParseFloat(a, 64)
		rb, _ := strconv.ParseFloat(b, 64)
		if distance := math.Abs(math.Log(x/y) - math.Log(ra/rb)); distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	if ratioOnly {
		return best, "", nil
	}
	switch shorter := math.Min(x, y); {
	case shorter >= 1080:
		resolution = "1080p"
	case shorter >= 720:
		resolution = "720p"
	default:
		resolution = "480p"
	}
	return best, resolution, nil
}

func positive(value float64) bool {
	return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
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

func cleanUpstreamText(provider Provider, message string) string {
	message = redactText(message, provider)
	message = upstreamURL.ReplaceAllString(message, "[URL]")
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return message
}

func upstreamDetails(provider Provider, body []byte) string {
	message := cleanUpstreamText(provider, vendorMessage(body))
	if message == "" {
		return ""
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
