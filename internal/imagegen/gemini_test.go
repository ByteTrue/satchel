package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type geminiGoldenRequest struct {
	Contents []struct {
		Role  string `json:"role"`
		Parts []struct {
			Text       string `json:"text"`
			InlineData *struct {
				MIMEType string `json:"mimeType"`
				Data     string `json:"data"`
			} `json:"inlineData"`
		} `json:"parts"`
	} `json:"contents"`
	GenerationConfig struct {
		ResponseModalities []string `json:"responseModalities"`
		CandidateCount     int      `json:"candidateCount"`
		ImageConfig        struct {
			AspectRatio string `json:"aspectRatio"`
		} `json:"imageConfig"`
	} `json:"generationConfig"`
}

func TestGeminiNativeGoldenAndMultiImageLoop(t *testing.T) {
	const apiKey = "gemini-test-key"
	ref := writeImage(t, "reference.png", testPNG)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1beta/models/gemini-2.5-flash-image:generateContent" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != apiKey {
			t.Errorf("x-goog-api-key = %q", got)
		}
		if got := r.Header.Get("X-Custom"); got != "custom-value" {
			t.Errorf("X-Custom = %q", got)
		}
		var payload geminiGoldenRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(payload.Contents) != 1 || payload.Contents[0].Role != "user" || len(payload.Contents[0].Parts) != 2 {
			t.Errorf("contents = %#v", payload.Contents)
		} else {
			parts := payload.Contents[0].Parts
			if parts[0].Text != "make it blue\n\nAspect ratio: 3:2." {
				t.Errorf("prompt part = %q", parts[0].Text)
			}
			if parts[1].InlineData == nil || parts[1].InlineData.MIMEType != "image/png" || parts[1].InlineData.Data != base64.StdEncoding.EncodeToString(testPNG) {
				t.Errorf("reference part = %#v", parts[1].InlineData)
			}
		}
		config := payload.GenerationConfig
		if config.CandidateCount != 1 || len(config.ResponseModalities) != 2 || config.ResponseModalities[0] != "TEXT" || config.ResponseModalities[1] != "IMAGE" || config.ImageConfig.AspectRatio != "3:2" {
			t.Errorf("generationConfig = %#v", config)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`,
			base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer server.Close()

	images, err := Generate(context.Background(), Provider{
		API: "gemini", BaseURL: server.URL, APIKey: apiKey, Model: "gemini-2.5-flash-image",
		Headers: map[string]string{"X-Custom": "custom-value"}, HTTPClient: server.Client(),
	}, Request{Prompt: "make it blue", Images: []string{ref}, N: 2, Size: "1536x1024"})
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	if len(images) != 2 || !bytes.Equal(images[0].Data, testPNG) || !bytes.Equal(images[1].Data, testPNG) {
		t.Fatalf("images = %#v", images)
	}
}

func TestGeminiPartialSuccessAndSnakeCaseResponse(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"quota"}}`)
			return
		}
		// A thought image is a draft and must be ignored. The real image uses
		// the proto/snake_case response spelling accepted by the TS adapter.
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"thought":true,"inlineData":{"data":"not-base64"}},{"inline_data":{"mime_type":"image/png","data":%q}}]}}]}`,
			base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer server.Close()

	images, err := Generate(context.Background(), Provider{
		API: "gemini", BaseURL: server.URL, Model: "gemini-image", HTTPClient: server.Client(),
	}, Request{Prompt: "draw", N: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || !bytes.Equal(images[0].Data, testPNG) {
		t.Fatalf("images = %#v", images)
	}
}
