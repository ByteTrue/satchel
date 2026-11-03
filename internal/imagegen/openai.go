// OpenAI Images protocol selectively adapted from Magpie
// internal/gateway/draw.go (drawImages/readImagesAnswer) at commit
// 4534561f6bb8f8353a67801fbe864a6502517022.
// Magpie is Copyright (c) 2026 yetone, MIT licensed.
package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
)

func generateOpenAI(ctx context.Context, provider Provider, request Request, refs []referenceImage, downloader *imageDownloader) ([]Image, error) {
	base, err := normalizeBase(provider.BaseURL, "https://api.openai.com/v1", "/v1")
	if err != nil {
		return nil, err
	}
	var body []byte
	var contentType, endpoint string
	if len(refs) == 0 {
		payload := map[string]any{"model": provider.Model, "prompt": request.Prompt, "n": request.N}
		if request.Size != "" {
			payload["size"] = request.Size
		}
		if request.Quality != "" {
			payload["quality"] = request.Quality
		}
		if request.Background != "" {
			payload["background"] = request.Background
		}
		// DALL-E returns URLs unless base64 is explicit; gpt-image rejects it.
		if strings.Contains(strings.ToLower(provider.Model), "dall-e") {
			payload["response_format"] = "b64_json"
		}
		body, err = json.Marshal(payload)
		contentType, endpoint = "application/json", base+"/images/generations"
	} else {
		body, contentType, err = openAIEditBody(provider.Model, request, refs)
		endpoint = base + "/images/edits"
	}
	if err != nil {
		return nil, err
	}
	auth := ""
	if provider.APIKey != "" {
		auth = "Bearer " + provider.APIKey
	}
	responseBody, err := post(ctx, provider, endpoint, contentType, body, "Authorization", auth)
	if err != nil {
		return nil, err
	}
	return readOpenAIImages(ctx, provider, responseBody, downloader)
}

func openAIEditBody(model string, request Request, refs []referenceImage) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	fields := map[string]string{
		"model": model, "prompt": request.Prompt, "n": strconv.Itoa(request.N), "size": request.Size,
		"quality": request.Quality, "background": request.Background,
	}
	for key, value := range fields {
		if value != "" {
			if err := writer.WriteField(key, value); err != nil {
				return nil, "", err
			}
		}
	}
	if strings.Contains(strings.ToLower(model), "dall-e") {
		if err := writer.WriteField("response_format", "b64_json"); err != nil {
			return nil, "", err
		}
	}
	field := "image"
	if len(refs) > 1 {
		field = "image[]"
	}
	for i, ref := range refs {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename="image%d%s"`, field, i, imageExtension(ref.mimeType)))
		header.Set("Content-Type", ref.mimeType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(ref.data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func readOpenAIImages(ctx context.Context, provider Provider, body []byte, downloader *imageDownloader) ([]Image, error) {
	var response struct {
		Data []struct {
			B64           string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
			MediaType     string `json:"media_type"`
			MIMEType      string `json:"mime_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("%s response is not valid Images JSON: %w", provider.API, err)
	}
	images := make([]Image, 0, len(response.Data))
	var failures []error
	for _, entry := range response.Data {
		if len(images) >= maxImages {
			break
		}
		declared := entry.MediaType
		if declared == "" {
			declared = entry.MIMEType
		}
		var image Image
		var err error
		switch {
		case entry.B64 != "":
			image, err = inlineImage(entry.B64, declared)
		case entry.URL != "":
			image, err = materializeImage(ctx, downloader, entry.URL, declared)
		default:
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s returned an invalid image: %w", provider.API, err))
			continue
		}
		image.RevisedPrompt = redactText(entry.RevisedPrompt, provider)
		images = append(images, image)
	}
	if len(images) == 0 {
		if len(failures) > 0 {
			return nil, errors.Join(failures...)
		}
		return nil, fmt.Errorf("%s returned no image data%s", provider.API, upstreamDetails(provider, body))
	}
	// Match Gemini's partial-success behavior: preserve every usable image and
	// report failures only when the response yielded none.
	return images, nil
}

func imageExtension(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}
