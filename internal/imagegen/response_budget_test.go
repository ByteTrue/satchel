package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInlineAndDataURLGeneratedImageSizeLimit(t *testing.T) {
	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, int(maxGeneratedImageBytes)-7)...)
	if int64(len(data)) != maxGeneratedImageBytes+1 {
		t.Fatalf("fixture size = %d", len(data))
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	for _, test := range []struct {
		name string
		call func() error
	}{
		{name: "base64", call: func() error { _, err := inlineImage(encoded, "image/png"); return err }},
		{name: "data URL", call: func() error { _, err := dataURLImage("data:image/png;base64," + encoded); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("oversized image error = %v", err)
			}
		})
	}
}

func TestUpstreamSignedURLsNeverAppearInErrors(t *testing.T) {
	const signed = "https://cdn.example.test/image.png?X-Amz-Signature=do-not-leak"
	const signedUpper = "HTTPS://cdn.example.test/image.png?X-Amz-Signature=do-not-leak"
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "HTTP error", status: 502, body: fmt.Sprintf(`{"error":{"message":"could not fetch %s"}}`, signed)},
		{name: "successful response without images", status: 200, body: fmt.Sprintf(`{"data":[],"error":{"message":"could not fetch %s"}}`, signed)},
		{name: "HTTP error uppercase scheme", status: 502, body: fmt.Sprintf(`{"error":{"message":"could not fetch %s"}}`, signedUpper)},
		{name: "no images uppercase scheme", status: 200, body: fmt.Sprintf(`{"data":[],"error":{"message":"could not fetch %s"}}`, signedUpper)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := Generate(context.Background(), Provider{API: "openai", BaseURL: server.URL, Model: "image-test"}, Request{Prompt: "test"})
			if err == nil {
				t.Fatal("generation unexpectedly succeeded")
			}
			if strings.Contains(err.Error(), "do-not-leak") || strings.Contains(err.Error(), "cdn.example.test") {
				t.Fatalf("signed URL leaked: %v", err)
			}
			if !strings.Contains(err.Error(), "[URL]") {
				t.Fatalf("URL was not visibly sanitized: %v", err)
			}
		})
	}
}

func TestProviderResponsesCannotExceedImageCountBudget(t *testing.T) {
	for _, api := range []string{"openai", "dashscope"} {
		t.Run(api, func(t *testing.T) {
			var ninthDownload atomic.Bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ninthDownload.Store(true)
				_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
			}))
			defer target.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if api == "openai" {
					entries := make([]map[string]any, 9)
					for i := range 8 {
						entries[i] = map[string]any{"b64_json": base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))}
					}
					entries[8] = map[string]any{"url": target.URL}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": entries})
					return
				}
				parts := make([]map[string]any, 9)
				for i := range 8 {
					parts[i] = map[string]any{"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))}
				}
				parts[8] = map[string]any{"image": target.URL}
				_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": parts}}}}})
			}))
			defer provider.Close()
			images, err := Generate(context.Background(), Provider{API: api, BaseURL: provider.URL, Model: "image-test"}, Request{Prompt: "test", N: 1})
			if err != nil || len(images) != maxImages {
				t.Fatalf("Generate returned %d images, %v", len(images), err)
			}
			if ninthDownload.Load() {
				t.Fatal("ninth image URL was downloaded")
			}
		})
	}
}
