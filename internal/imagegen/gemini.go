// Gemini native generateContent protocol and one-request-per-image loop are
// selectively adapted from Magpie internal/gateway/draw.go
// (drawGemini/eachDrawing/aspectOf) at commit
// 4534561f6bb8f8353a67801fbe864a6502517022.
// Magpie is Copyright (c) 2026 yetone, MIT licensed.
package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

func generateGemini(ctx context.Context, provider Provider, request Request, refs []referenceImage) ([]Image, error) {
	base, err := normalizeBase(provider.BaseURL, "https://generativelanguage.googleapis.com/v1beta", "/v1beta")
	if err != nil {
		return nil, err
	}
	base = strings.TrimSuffix(base, "/openai")
	endpoint := base + "/models/" + url.PathEscape(provider.Model) + ":generateContent"
	ratio := geminiAspect(request.Size)
	prompt := request.Prompt
	if ratio != "" {
		prompt += "\n\nAspect ratio: " + ratio + "."
	}
	parts := []map[string]any{{"text": prompt}}
	for _, ref := range refs {
		parts = append(parts, map[string]any{"inlineData": map[string]string{
			"mimeType": ref.mimeType,
			"data":     base64.StdEncoding.EncodeToString(ref.data),
		}})
	}
	config := map[string]any{
		"responseModalities": []string{"TEXT", "IMAGE"},
		"candidateCount":     1,
	}
	if ratio != "" {
		config["imageConfig"] = map[string]string{"aspectRatio": ratio}
	}
	body, err := json.Marshal(map[string]any{
		"contents":         []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": config,
	})
	if err != nil {
		return nil, err
	}

	type result struct {
		images []Image
		err    error
	}
	results := make([]result, request.N)
	var wg sync.WaitGroup
	for i := range request.N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responseBody, err := post(ctx, provider, endpoint, "application/json", body, "x-goog-api-key", provider.APIKey)
			if err != nil {
				results[i].err = err
				return
			}
			images, err := readGeminiImages(provider, responseBody)
			results[i] = result{images: images, err: err}
		}()
	}
	wg.Wait()

	images := make([]Image, 0, request.N)
	var firstErr error
	for _, result := range results {
		images = append(images, result.images...)
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
	}
	if len(images) > maxImages {
		images = images[:maxImages]
	}
	// Preserve Magpie's partial-success behavior: return any paid-for images;
	// only when every request produced none is the first failure returned.
	if len(images) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return images, nil
}

type geminiBlob struct {
	Data          string `json:"data"`
	MIMEType      string `json:"mimeType"`
	MIMETypeSnake string `json:"mime_type"`
}

func (b *geminiBlob) mimeType() string {
	if b.MIMEType != "" {
		return b.MIMEType
	}
	return b.MIMETypeSnake
}

func readGeminiImages(provider Provider, body []byte) ([]Image, error) {
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text            string      `json:"text"`
					Thought         bool        `json:"thought"`
					InlineData      *geminiBlob `json:"inlineData"`
					InlineDataSnake *geminiBlob `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("gemini response is not valid generateContent JSON: %w", err)
	}
	var images []Image
candidates:
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if len(images) >= maxImages {
				break candidates
			}
			if part.Thought {
				continue
			}
			blob := part.InlineData
			if blob == nil {
				blob = part.InlineDataSnake
			}
			if blob == nil || blob.Data == "" {
				continue
			}
			image, err := inlineImage(blob.Data, blob.mimeType())
			if err != nil {
				return nil, fmt.Errorf("gemini returned an invalid image: %w", err)
			}
			images = append(images, image)
		}
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("gemini returned no image data%s", upstreamDetails(provider, body))
	}
	return images, nil
}

func geminiAspect(size string) string {
	width, height, ok := strings.Cut(strings.ToLower(strings.TrimSpace(size)), "x")
	if !ok {
		if strings.Contains(size, ":") {
			return strings.TrimSpace(size)
		}
		return ""
	}
	w, errW := strconv.ParseFloat(width, 64)
	h, errH := strconv.ParseFloat(height, 64)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return ""
	}
	best, bestDistance := "", math.Inf(1)
	for _, ratio := range []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"} {
		a, b, _ := strings.Cut(ratio, ":")
		ra, _ := strconv.ParseFloat(a, 64)
		rb, _ := strconv.ParseFloat(b, 64)
		if distance := math.Abs(math.Log(w/h) - math.Log(ra/rb)); distance < bestDistance {
			best, bestDistance = ratio, distance
		}
	}
	return best
}
