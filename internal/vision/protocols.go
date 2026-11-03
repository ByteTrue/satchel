package vision

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const anthropicMaxTokens = 4096

type protocolRequest struct {
	endpoint string
	body     []byte
	headers  map[string]string
	parse    func([]byte) (string, error)
}

func buildRequest(provider Provider, images []image, question string) (protocolRequest, error) {
	switch provider.API {
	case "openai-completions":
		return buildChatRequest(provider, images, question)
	case "openai-responses":
		return buildResponsesRequest(provider, images, question)
	case "anthropic-messages":
		return buildAnthropicRequest(provider, images, question)
	case "google-generative-ai":
		return buildGeminiRequest(provider, images, question)
	default:
		return protocolRequest{}, errors.New("unsupported vision protocol")
	}
}

func buildChatRequest(provider Provider, images []image, question string) (protocolRequest, error) {
	base, err := normalizeBase(provider.BaseURL, "https://api.openai.com/v1", "/v1")
	if err != nil {
		return protocolRequest{}, err
	}
	content := make([]map[string]any, 0, len(images)+1)
	for _, image := range images {
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURL(image)},
		})
	}
	content = append(content, map[string]any{"type": "text", "text": question})
	body, err := json.Marshal(map[string]any{
		"model": provider.Model, "stream": false,
		"messages": []map[string]any{{"role": "user", "content": content}},
	})
	if err != nil {
		return protocolRequest{}, err
	}
	return protocolRequest{
		endpoint: base + "/chat/completions", body: body,
		headers: bearerHeaders(provider.APIKey), parse: parseChatAnswer,
	}, nil
}

func buildResponsesRequest(provider Provider, images []image, question string) (protocolRequest, error) {
	base, err := normalizeBase(provider.BaseURL, "https://api.openai.com/v1", "/v1")
	if err != nil {
		return protocolRequest{}, err
	}
	content := make([]map[string]any, 0, len(images)+1)
	for _, image := range images {
		content = append(content, map[string]any{
			"type": "input_image", "detail": "auto", "image_url": dataURL(image),
		})
	}
	content = append(content, map[string]any{"type": "input_text", "text": question})
	body, err := json.Marshal(map[string]any{
		"model": provider.Model, "stream": false, "store": false,
		"input": []map[string]any{{"type": "message", "role": "user", "content": content}},
	})
	if err != nil {
		return protocolRequest{}, err
	}
	return protocolRequest{
		endpoint: base + "/responses", body: body,
		headers: bearerHeaders(provider.APIKey), parse: parseResponsesAnswer,
	}, nil
}

func buildAnthropicRequest(provider Provider, images []image, question string) (protocolRequest, error) {
	base, err := normalizeBase(provider.BaseURL, "https://api.anthropic.com/v1", "/v1")
	if err != nil {
		return protocolRequest{}, err
	}
	content := make([]map[string]any, 0, len(images)+1)
	for _, image := range images {
		content = append(content, map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "base64", "media_type": image.mimeType, "data": image.data},
		})
	}
	content = append(content, map[string]any{"type": "text", "text": question})
	body, err := json.Marshal(map[string]any{
		"model": provider.Model, "stream": false, "max_tokens": anthropicMaxTokens,
		"messages": []map[string]any{{"role": "user", "content": content}},
	})
	if err != nil {
		return protocolRequest{}, err
	}
	headers := map[string]string{"anthropic-version": "2023-06-01"}
	if provider.APIKey != "" {
		headers["x-api-key"] = provider.APIKey
	}
	return protocolRequest{
		endpoint: base + "/messages", body: body, headers: headers, parse: parseAnthropicAnswer,
	}, nil
}

func buildGeminiRequest(provider Provider, images []image, question string) (protocolRequest, error) {
	base, err := normalizeBase(provider.BaseURL, "https://generativelanguage.googleapis.com/v1beta", "/v1beta")
	if err != nil {
		return protocolRequest{}, err
	}
	parts := make([]map[string]any, 0, len(images)+1)
	for _, image := range images {
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{"mimeType": image.mimeType, "data": image.data},
		})
	}
	parts = append(parts, map[string]any{"text": question})
	body, err := json.Marshal(map[string]any{
		"contents": []map[string]any{{"role": "user", "parts": parts}},
	})
	if err != nil {
		return protocolRequest{}, err
	}
	model := url.PathEscape(strings.TrimPrefix(provider.Model, "models/"))
	headers := map[string]string{}
	if provider.APIKey != "" {
		headers["x-goog-api-key"] = provider.APIKey
	}
	return protocolRequest{
		endpoint: base + "/models/" + model + ":generateContent", body: body,
		headers: headers, parse: parseGeminiAnswer,
	}, nil
}

func dataURL(image image) string {
	return "data:" + image.mimeType + ";base64," + image.data
}

func bearerHeaders(apiKey string) map[string]string {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return headers
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

func parseChatAnswer(body []byte) (string, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("invalid Chat Completions JSON: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", nil
	}
	raw := response.Choices[0].Message.Content
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("message content is not text or text parts: %w", err)
	}
	var texts []string
	for _, part := range parts {
		if part.Type == "" || part.Type == "text" || part.Type == "output_text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func parseResponsesAnswer(body []byte) (string, error) {
	var response struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("invalid Responses JSON: %w", err)
	}
	var texts []string
	for _, item := range response.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" || part.Type == "text" {
				texts = append(texts, part.Text)
			}
		}
	}
	if len(texts) == 0 {
		return response.OutputText, nil
	}
	return strings.Join(texts, "\n"), nil
}

func parseAnthropicAnswer(body []byte) (string, error) {
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("invalid Messages JSON: %w", err)
	}
	var texts []string
	for _, part := range response.Content {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func parseGeminiAnswer(body []byte) (string, error) {
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("invalid generateContent JSON: %w", err)
	}
	if len(response.Candidates) == 0 {
		return "", nil
	}
	var texts []string
	for _, part := range response.Candidates[0].Content.Parts {
		if !part.Thought && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}
