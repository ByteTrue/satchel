package imagegen

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testPNG = mustBase64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

func mustBase64(value string) []byte {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return data
}

func writeImage(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGenerateValidatesBeforeNetwork(t *testing.T) {
	provider := Provider{API: "openai", BaseURL: "http://127.0.0.1:1", Model: "gpt-image-1"}
	text := writeImage(t, "not-image.txt", []byte("not an image"))
	big := writeImage(t, "big.png", testPNG)
	if err := os.Truncate(big, maxReferenceBytes+1); err != nil {
		t.Fatal(err)
	}
	tooMany := make([]string, maxReferenceImages+1)
	for i := range tooMany {
		tooMany[i] = filepath.Join(string(filepath.Separator), fmt.Sprintf("%d.png", i))
	}

	tests := []struct {
		name     string
		provider Provider
		request  Request
		want     string
	}{
		{name: "provider API", provider: Provider{API: "other", Model: "m"}, request: Request{Prompt: "x"}, want: "unsupported"},
		{name: "model", provider: Provider{API: "openai"}, request: Request{Prompt: "x"}, want: "model"},
		{name: "prompt", provider: provider, request: Request{Prompt: "  "}, want: "prompt"},
		{name: "n", provider: provider, request: Request{Prompt: "x", N: maxImages + 1}, want: "at most 8"},
		{name: "reference count", provider: provider, request: Request{Prompt: "x", Images: tooMany}, want: "reference images"},
		{name: "relative reference", provider: provider, request: Request{Prompt: "x", Images: []string{"relative.png"}}, want: "absolute"},
		{name: "reference MIME", provider: provider, request: Request{Prompt: "x", Images: []string{text}}, want: "PNG, JPEG, GIF, or WebP"},
		{name: "reference size", provider: provider, request: Request{Prompt: "x", Images: []string{big}}, want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Generate(context.Background(), test.provider, test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestGenerateRejectsUnsupportedQualityAndBackgroundBeforeNetwork(t *testing.T) {
	for _, api := range []string{"gemini", "dashscope", "ark", "openrouter"} {
		for _, field := range []string{"quality", "background"} {
			t.Run(api+"/"+field, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected %s request: %s %s", api, r.Method, r.URL.Path)
				}))
				defer server.Close()
				request := Request{Prompt: "draw"}
				if field == "quality" {
					request.Quality = "high"
				} else {
					request.Background = "transparent"
				}
				_, err := Generate(context.Background(), Provider{
					API: api, BaseURL: server.URL, Model: "image-model", HTTPClient: server.Client(),
				}, request)
				if err == nil || !strings.Contains(err.Error(), "unsupported") ||
					!strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), api) {
					t.Fatalf("Generate() error = %v, want unsupported %s for %s", err, field, api)
				}
			})
		}
	}
}

func TestGenerateRedactsUpstreamErrors(t *testing.T) {
	const apiKey = "sk-live-test-secret"
	const headerSecret = "custom-header-secret"
	for _, api := range []string{"openai", "gemini", "dashscope", "ark", "openrouter"} {
		for _, status := range []int{http.StatusTooManyRequests, http.StatusOK} {
			t.Run(fmt.Sprintf("%s/status_%d", api, status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					fmt.Fprintf(w, `{"error":{"message":%q}}`, "rejected "+apiKey+" and "+headerSecret)
				}))
				defer server.Close()
				_, err := Generate(context.Background(), Provider{
					API: api, BaseURL: server.URL, APIKey: apiKey, Model: "image-model",
					Headers: map[string]string{"X-Custom": headerSecret}, HTTPClient: server.Client(),
				}, Request{Prompt: "draw"})
				if err == nil {
					t.Fatal("Generate() succeeded, want an upstream error")
				}
				message := err.Error()
				if strings.Contains(message, apiKey) || strings.Contains(message, headerSecret) {
					t.Fatalf("error leaked a secret: %s", message)
				}
				if !strings.Contains(message, "[REDACTED]") {
					t.Fatalf("error did not show redaction: %s", message)
				}
			})
		}
	}
}
