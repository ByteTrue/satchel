// OpenRouter and Ark protocol details are selectively adapted from Magpie
// internal/gateway/draw.go and internal/gateway/draw_volcengine.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022. The JSON shapes also match
// pi-package-mono/packages/pi-image-gen/src/providers/{dashscope,ark,openrouter}.ts.
// Magpie is Copyright (c) 2026 yetone, MIT licensed.
package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func referenceDataURL(ref referenceImage) string {
	return "data:" + ref.mimeType + ";base64," + base64.StdEncoding.EncodeToString(ref.data)
}

func bearerAuth(provider Provider) string {
	if provider.APIKey == "" {
		return ""
	}
	return "Bearer " + provider.APIKey
}

func generateDashscope(ctx context.Context, provider Provider, request Request, refs []referenceImage, downloader *imageDownloader) ([]Image, error) {
	base, err := normalizeBase(provider.BaseURL, "https://dashscope.aliyuncs.com/api/v1", "/api/v1")
	if err != nil {
		return nil, err
	}
	content := make([]map[string]any, 0, len(refs)+1)
	for _, ref := range refs {
		content = append(content, map[string]any{"image": referenceDataURL(ref)})
	}
	content = append(content, map[string]any{"text": request.Prompt})
	parameters := map[string]any{"n": request.N}
	if request.Size != "" {
		parameters["size"] = request.Size
	}
	body, err := json.Marshal(map[string]any{
		"model":      provider.Model,
		"input":      map[string]any{"messages": []map[string]any{{"role": "user", "content": content}}},
		"parameters": parameters,
	})
	if err != nil {
		return nil, err
	}
	endpoint := base + "/services/aigc/multimodal-generation/generation"
	responseBody, err := post(ctx, provider, endpoint, "application/json", body, "Authorization", bearerAuth(provider))
	if err != nil {
		return nil, err
	}
	return readDashscopeImages(ctx, provider, responseBody, downloader)
}

type dashscopeContent struct {
	Image    string          `json:"image"`
	ImageURL json.RawMessage `json:"image_url"`
	Text     string          `json:"text"`
}

func (p dashscopeContent) candidate() string {
	if len(p.ImageURL) > 0 {
		var value string
		if json.Unmarshal(p.ImageURL, &value) == nil {
			return value
		}
		var object struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(p.ImageURL, &object) == nil && object.URL != "" {
			return object.URL
		}
	}
	return p.Image
}

func readDashscopeImages(ctx context.Context, provider Provider, body []byte, downloader *imageDownloader) ([]Image, error) {
	var response struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content []dashscopeContent `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("dashscope response is not valid multimodal-generation JSON: %w", err)
	}
	var images []Image
	var failures []error
choices:
	for _, choice := range response.Output.Choices {
		for _, part := range choice.Message.Content {
			if len(images) >= maxImages {
				break choices
			}
			candidate := part.candidate()
			if candidate == "" {
				continue
			}
			image, err := materializeImage(ctx, downloader, candidate, "")
			if err != nil {
				failures = append(failures, fmt.Errorf("dashscope returned an invalid image: %w", err))
				continue
			}
			images = append(images, image)
		}
	}
	if len(images) == 0 {
		if len(failures) > 0 {
			return nil, errors.Join(failures...)
		}
		return nil, fmt.Errorf("dashscope returned no image data%s", upstreamDetails(provider, body))
	}
	return images, nil
}

func generateArk(ctx context.Context, provider Provider, request Request, refs []referenceImage, downloader *imageDownloader) ([]Image, error) {
	base, err := normalizeBase(provider.BaseURL, "https://ark.cn-beijing.volces.com/api/v3", "/api/v3")
	if err != nil {
		return nil, err
	}
	size, err := arkSize(request.Size)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"model": provider.Model, "prompt": request.Prompt, "n": request.N}
	if size != "" {
		payload["size"] = size
	}
	if len(refs) > 0 {
		images := make([]string, 0, len(refs))
		for _, ref := range refs {
			images = append(images, referenceDataURL(ref))
		}
		payload["image"] = images
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	responseBody, err := post(ctx, provider, base+"/images/generations", "application/json", body, "Authorization", bearerAuth(provider))
	if err != nil {
		return nil, err
	}
	return readOpenAIImages(ctx, provider, responseBody, downloader)
}

var arkRatioSizes = map[string]string{
	"1:1": "2048x2048", "4:3": "2304x1728", "3:4": "1728x2304",
	"16:9": "2848x1600", "9:16": "1600x2848", "3:2": "2496x1664",
	"2:3": "1664x2496", "21:9": "3136x1344",
}

func arkSize(size string) (string, error) {
	size = strings.TrimSpace(size)
	if size == "" || strings.EqualFold(size, "auto") {
		return "", nil
	}
	if strings.Contains(size, ":") {
		if mapped, ok := arkRatioSizes[strings.ReplaceAll(size, " ", "")]; ok {
			return mapped, nil
		}
		return "", fmt.Errorf("Ark size %q is not a supported ratio; use WIDTHxHEIGHT or leave size out", size)
	}
	return size, nil
}

func generateOpenRouter(ctx context.Context, provider Provider, request Request, refs []referenceImage, downloader *imageDownloader) ([]Image, error) {
	base, err := normalizeBase(provider.BaseURL, "https://openrouter.ai/api/v1", "/api/v1")
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"model": provider.Model, "prompt": request.Prompt, "n": request.N}
	if request.Size != "" {
		payload["size"] = request.Size
	}
	if len(refs) > 0 {
		inputs := make([]map[string]any, 0, len(refs))
		for _, ref := range refs {
			inputs = append(inputs, map[string]any{"image_url": map[string]string{"url": referenceDataURL(ref)}})
		}
		payload["input_references"] = inputs
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	responseBody, err := post(ctx, provider, base+"/images", "application/json", body, "Authorization", bearerAuth(provider))
	if err != nil {
		return nil, err
	}
	return readOpenAIImages(ctx, provider, responseBody, downloader)
}
