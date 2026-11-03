package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenAIGenerationsGolden(t *testing.T) {
	const apiKey = "openai-test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/images/generations" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Custom"); got != "custom-value" {
			t.Errorf("X-Custom = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload["model"] != "gpt-image-2" || payload["prompt"] != "a cat" || payload["n"] != float64(2) || payload["size"] != "1024x1024" {
			t.Errorf("payload = %#v", payload)
		}
		if payload["quality"] != "high" || payload["background"] != "transparent" {
			t.Errorf("quality/background = %#v/%#v", payload["quality"], payload["background"])
		}
		if _, exists := payload["response_format"]; exists {
			t.Errorf("gpt-image payload contains response_format: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"b64_json":%q,"revised_prompt":"first"},{"b64_json":%q,"revised_prompt":"second"}]}`,
			base64.StdEncoding.EncodeToString(testPNG), base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer server.Close()

	images, err := Generate(context.Background(), Provider{
		API: "openai", BaseURL: server.URL, APIKey: apiKey, Model: "gpt-image-2",
		Headers: map[string]string{"X-Custom": "custom-value"}, HTTPClient: server.Client(),
	}, Request{Prompt: "a cat", N: 2, Size: "1024x1024", Quality: "high", Background: "transparent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 {
		t.Fatalf("len(images) = %d", len(images))
	}
	for i, image := range images {
		if !bytes.Equal(image.Data, testPNG) || image.MIMEType != "image/png" {
			t.Errorf("image %d = %d bytes, %s", i, len(image.Data), image.MIMEType)
		}
	}
	if images[0].RevisedPrompt != "first" || images[1].RevisedPrompt != "second" {
		t.Errorf("revised prompts = %q, %q", images[0].RevisedPrompt, images[1].RevisedPrompt)
	}
}

func TestOpenAIMultiImageEditGolden(t *testing.T) {
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe0}, make([]byte, 20)...)
	first := writeImage(t, "first.png", testPNG)
	second := writeImage(t, "second.jpg", jpeg)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "multipart/form-data" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for field, want := range map[string]string{
			"model": "gpt-image-2", "prompt": "combine", "n": "1", "size": "1536x1024",
			"quality": "medium", "background": "opaque",
		} {
			if got := r.FormValue(field); got != want {
				t.Errorf("form %s = %q, want %q", field, got, want)
			}
		}
		files := r.MultipartForm.File["image[]"]
		if len(files) != 2 {
			t.Errorf("image[] has %d files", len(files))
		}
		for i, want := range [][]byte{testPNG, jpeg} {
			if i >= len(files) {
				continue
			}
			part, err := files[i].Open()
			if err != nil {
				t.Errorf("open part: %v", err)
				continue
			}
			got, readErr := io.ReadAll(part)
			part.Close()
			if readErr != nil || !bytes.Equal(got, want) {
				t.Errorf("image %d bytes differ (read error %v)", i, readErr)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer server.Close()

	images, err := Generate(context.Background(), Provider{
		API: "openai", BaseURL: server.URL, Model: "gpt-image-2", HTTPClient: server.Client(),
	}, Request{Prompt: "combine", Images: []string{first, second}, Size: "1536x1024", Quality: "medium", Background: "opaque"})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || !bytes.Equal(images[0].Data, testPNG) {
		t.Fatalf("images = %#v", images)
	}
}

func TestOpenAIOmitsEmptyQualityAndBackground(t *testing.T) {
	ref := writeImage(t, "reference.png", testPNG)
	tests := []struct {
		name   string
		path   string
		images []string
	}{
		{name: "generations", path: "/v1/images/generations"},
		{name: "edits", path: "/v1/images/edits", images: []string{ref}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Errorf("path = %q, want %q", r.URL.Path, test.path)
				}
				if len(test.images) == 0 {
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Errorf("decode request: %v", err)
					}
					for _, field := range []string{"quality", "background"} {
						if _, exists := payload[field]; exists {
							t.Errorf("empty %s was sent: %s", field, payload[field])
						}
					}
				} else if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Errorf("ParseMultipartForm: %v", err)
				} else {
					defer r.MultipartForm.RemoveAll()
					for _, field := range []string{"quality", "background"} {
						if _, exists := r.MultipartForm.Value[field]; exists {
							t.Errorf("empty %s was sent: %#v", field, r.MultipartForm.Value[field])
						}
					}
				}
				fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(testPNG))
			}))
			defer server.Close()

			_, err := Generate(context.Background(), Provider{
				API: "openai", BaseURL: server.URL, Model: "gpt-image-2", HTTPClient: server.Client(),
			}, Request{Prompt: "draw", Images: test.images})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenAIInlineDataURLAndRemoteURLBoundary(t *testing.T) {
	t.Run("data URL", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data":[{"url":%q}]}`, "DaTa:image/png;BASE64,"+base64.StdEncoding.EncodeToString(testPNG))
		}))
		defer server.Close()
		images, err := Generate(context.Background(), Provider{API: "openai", BaseURL: server.URL, Model: "m"}, Request{Prompt: "x"})
		if err != nil || len(images) != 1 || !bytes.Equal(images[0].Data, testPNG) {
			t.Fatalf("images = %#v, err = %v", images, err)
		}
	})

	t.Run("private remote URL is rejected before download", func(t *testing.T) {
		var downloads atomic.Int32
		private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			downloads.Add(1)
		}))
		defer private.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data":[{"url":%q}]}`, private.URL+"/secret.png")
		}))
		defer server.Close()
		_, err := Generate(context.Background(), Provider{API: "openai", BaseURL: server.URL, Model: "m"}, Request{Prompt: "x"})
		if err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Fatalf("Generate() error = %v", err)
		}
		if got := downloads.Load(); got != 0 {
			t.Fatalf("private remote URL was fetched %d times", got)
		}
	})
}
