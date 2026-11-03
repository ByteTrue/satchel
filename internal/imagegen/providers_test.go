package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashscopeGolden(t *testing.T) {
	const apiKey = "dashscope-test-key"
	first := writeImage(t, "first.png", testPNG)
	second := writeImage(t, "second.png", testPNG)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/services/aigc/multimodal-generation/generation" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", got)
		}
		var payload struct {
			Model string `json:"model"`
			Input struct {
				Messages []struct {
					Role    string `json:"role"`
					Content []struct {
						Image string `json:"image"`
						Text  string `json:"text"`
					} `json:"content"`
				} `json:"messages"`
			} `json:"input"`
			Parameters struct {
				N    int    `json:"n"`
				Size string `json:"size"`
			} `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Model != "qwen-image-plus" || payload.Parameters.N != 2 || payload.Parameters.Size != "1024x1024" {
			t.Errorf("payload = %#v", payload)
		}
		if len(payload.Input.Messages) != 1 || payload.Input.Messages[0].Role != "user" {
			t.Fatalf("messages = %#v", payload.Input.Messages)
		}
		content := payload.Input.Messages[0].Content
		if len(content) != 3 || content[0].Image != referenceDataURL(referenceImage{data: testPNG, mimeType: "image/png"}) ||
			content[1].Image != content[0].Image || content[2].Text != "combine into a landscape" {
			t.Errorf("content order = %#v", content)
		}
		encoded := base64.StdEncoding.EncodeToString(testPNG)
		fmt.Fprintf(w, `{"output":{"choices":[{"message":{"content":[{"text":"done"},{"image":%q},{"image_url":{"url":%q}}]}}]}}`,
			"data:image/png;base64,"+encoded, "data:image/png;base64,"+encoded)
	}))
	defer server.Close()

	images, err := Generate(context.Background(), Provider{
		API: "dashscope", BaseURL: server.URL, APIKey: apiKey, Model: "qwen-image-plus", HTTPClient: server.Client(),
	}, Request{Prompt: "combine into a landscape", Images: []string{first, second}, N: 2, Size: "1024x1024"})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || !bytes.Equal(images[0].Data, testPNG) || !bytes.Equal(images[1].Data, testPNG) {
		t.Fatalf("images = %#v", images)
	}
}

func TestArkGoldenUsesJSONArrayAndRatioSize(t *testing.T) {
	const apiKey = "ark-test-key"
	ref := writeImage(t, "reference.png", testPNG)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/images/generations" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", got)
		}
		var payload struct {
			Model  string   `json:"model"`
			Prompt string   `json:"prompt"`
			N      int      `json:"n"`
			Size   string   `json:"size"`
			Images []string `json:"image"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode JSON request (must not be multipart): %v", err)
		}
		if payload.Model != "doubao-seedream" || payload.Prompt != "redraw" || payload.N != 2 || payload.Size != "2848x1600" {
			t.Errorf("payload = %#v", payload)
		}
		wantRef := referenceDataURL(referenceImage{data: testPNG, mimeType: "image/png"})
		if len(payload.Images) != 1 || payload.Images[0] != wantRef {
			t.Errorf("image = %#v, want [%q]", payload.Images, wantRef)
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer server.Close()

	images, err := Generate(context.Background(), Provider{
		API: "ark", BaseURL: server.URL, APIKey: apiKey, Model: "doubao-seedream", HTTPClient: server.Client(),
	}, Request{Prompt: "redraw", Images: []string{ref}, N: 2, Size: "16:9"})
	if err != nil || len(images) != 1 || !bytes.Equal(images[0].Data, testPNG) {
		t.Fatalf("images = %#v, err = %v", images, err)
	}
}

func TestOpenRouterGolden(t *testing.T) {
	const apiKey = "openrouter-test-key"
	ref := writeImage(t, "reference.png", testPNG)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/images" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q", got)
		}
		var payload struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			N      int    `json:"n"`
			Size   string `json:"size"`
			Refs   []struct {
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"input_references"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Model != "google/gemini-image" || payload.Prompt != "make blue" || payload.N != 2 || payload.Size != "1536x1024" {
			t.Errorf("payload = %#v", payload)
		}
		wantRef := referenceDataURL(referenceImage{data: testPNG, mimeType: "image/png"})
		if len(payload.Refs) != 1 || payload.Refs[0].ImageURL.URL != wantRef {
			t.Errorf("input_references = %#v", payload.Refs)
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer server.Close()

	images, err := Generate(context.Background(), Provider{
		API: "openrouter", BaseURL: server.URL, APIKey: apiKey, Model: "google/gemini-image", HTTPClient: server.Client(),
	}, Request{Prompt: "make blue", Images: []string{ref}, N: 2, Size: "1536x1024"})
	if err != nil || len(images) != 1 || !bytes.Equal(images[0].Data, testPNG) {
		t.Fatalf("images = %#v, err = %v", images, err)
	}
}

func TestArkRejectsUnknownRatio(t *testing.T) {
	if _, err := arkSize("7:5"); err == nil {
		t.Fatal("unknown Ark ratio was accepted")
	}
}
