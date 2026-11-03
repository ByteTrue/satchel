package vision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

var (
	testPNG  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x01}
	testJPEG = []byte{0xff, 0xd8, 0xff, 0x02}
)

type capturedRequest struct {
	uri    string
	header http.Header
	body   []byte
	err    error
}

func TestProtocolGolden(t *testing.T) {
	tests := []struct {
		name        string
		api         string
		model       string
		question    string
		wantURI     string
		response    string
		authName    string
		authValue   string
		wantVersion string
	}{
		{
			name: "chat", api: "openai-completions", model: "vision-model",
			wantURI: "/v1/chat/completions", response: `{"choices":[{"message":{"content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}]}`,
			authName: "Authorization", authValue: "Bearer test-key",
		},
		{
			name: "responses", api: "openai-responses", model: "vision-model", question: "What is shown?",
			wantURI: "/v1/responses", response: `{"output":[{"type":"message","content":[{"type":"output_text","text":"first"},{"type":"output_text","text":"second"}]}]}`,
			authName: "Authorization", authValue: "Bearer test-key",
		},
		{
			name: "anthropic", api: "anthropic-messages", model: "vision-model", question: "What is shown?",
			wantURI: "/v1/messages", response: `{"content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"first"},{"type":"text","text":"second"}]}`,
			authName: "x-api-key", authValue: "test-key", wantVersion: "2023-06-01",
		},
		{
			name: "gemini", api: "google-generative-ai", model: "models/gemini-test", question: "What is shown?",
			wantURI: "/v1beta/models/gemini-test:generateContent", response: `{"candidates":[{"content":{"parts":[{"text":"hidden","thought":true},{"text":"first"},{"text":"second"}]}}]}`,
			authName: "x-goog-api-key", authValue: "test-key",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := make(chan capturedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				captured <- capturedRequest{uri: r.URL.RequestURI(), header: r.Header.Clone(), body: body, err: err}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()

			dir := t.TempDir()
			pngPath := filepath.Join(dir, "first.jpg") // Extension deliberately disagrees with magic.
			jpegPath := filepath.Join(dir, "second.bin")
			writeTestImage(t, pngPath, testPNG)
			writeTestImage(t, jpegPath, testJPEG)
			answer, err := Ask(context.Background(), Provider{
				API: test.api, BaseURL: server.URL, APIKey: "test-key", Model: test.model,
				Headers: map[string]string{"X-Custom": "custom-value"},
			}, []string{pngPath, jpegPath}, test.question)
			if err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if answer.Text != "first\nsecond" {
				t.Fatalf("answer = %q, want %q", answer.Text, "first\nsecond")
			}

			got := <-captured
			if got.err != nil {
				t.Fatalf("read request: %v", got.err)
			}
			if got.uri != test.wantURI {
				t.Errorf("URI = %q, want %q", got.uri, test.wantURI)
			}
			if value := got.header.Get("Content-Type"); value != "application/json" {
				t.Errorf("Content-Type = %q", value)
			}
			if value := got.header.Get("X-Custom"); value != "custom-value" {
				t.Errorf("X-Custom = %q", value)
			}
			if value := got.header.Get(test.authName); value != test.authValue {
				t.Errorf("%s = %q, want %q", test.authName, value, test.authValue)
			}
			if value := got.header.Get("anthropic-version"); value != test.wantVersion {
				t.Errorf("anthropic-version = %q, want %q", value, test.wantVersion)
			}

			question := test.question
			if strings.TrimSpace(question) == "" {
				question = defaultQuestion
			}
			png64 := base64.StdEncoding.EncodeToString(testPNG)
			jpeg64 := base64.StdEncoding.EncodeToString(testJPEG)
			var want any
			switch test.api {
			case "openai-completions":
				want = map[string]any{
					"model": test.model, "stream": false,
					"messages": []any{map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + png64}},
						map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + jpeg64}},
						map[string]any{"type": "text", "text": question},
					}}},
				}
			case "openai-responses":
				want = map[string]any{
					"model": test.model, "stream": false, "store": false,
					"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{
						map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64," + png64},
						map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/jpeg;base64," + jpeg64},
						map[string]any{"type": "input_text", "text": question},
					}}},
				}
			case "anthropic-messages":
				want = map[string]any{
					"model": test.model, "stream": false, "max_tokens": anthropicMaxTokens,
					"messages": []any{map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": png64}},
						map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": jpeg64}},
						map[string]any{"type": "text", "text": question},
					}}},
				}
			case "google-generative-ai":
				want = map[string]any{
					"contents": []any{map[string]any{"role": "user", "parts": []any{
						map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": png64}},
						map[string]any{"inlineData": map[string]any{"mimeType": "image/jpeg", "data": jpeg64}},
						map[string]any{"text": question},
					}}},
				}
			}
			if body := jsonValue(t, got.body); !reflect.DeepEqual(body, jsonValue(t, mustMarshal(t, want))) {
				t.Errorf("payload mismatch\n got: %s\nwant: %s", mustMarshal(t, body), mustMarshal(t, want))
			}
		})
	}
}

func TestDefaultProtocolEndpoints(t *testing.T) {
	tests := []struct {
		api  string
		want string
	}{
		{"openai-completions", "https://api.openai.com/v1/chat/completions"},
		{"openai-responses", "https://api.openai.com/v1/responses"},
		{"anthropic-messages", "https://api.anthropic.com/v1/messages"},
		{"google-generative-ai", "https://generativelanguage.googleapis.com/v1beta/models/vision-model:generateContent"},
	}
	for _, test := range tests {
		t.Run(test.api, func(t *testing.T) {
			request, err := buildRequest(Provider{API: test.api, Model: "vision-model"}, []image{{data: "aA==", mimeType: "image/png"}}, "q")
			if err != nil {
				t.Fatal(err)
			}
			if request.endpoint != test.want {
				t.Fatalf("endpoint = %q, want %q", request.endpoint, test.want)
			}
		})
	}
}

func TestResponseTextVariants(t *testing.T) {
	tests := []struct {
		name  string
		parse func([]byte) (string, error)
		body  string
		want  string
	}{
		{"chat string", parseChatAnswer, `{"choices":[{"message":{"content":"  plain text  "}}]}`, "  plain text  "},
		{"responses output_text fallback", parseResponsesAnswer, `{"output_text":"plain text"}`, "plain text"},
		{"responses skips reasoning items", parseResponsesAnswer, `{"output":[{"type":"reasoning","content":[{"type":"output_text","text":"hidden"}]},{"type":"message","content":[{"type":"output_text","text":"visible"}]}]}`, "visible"},
		{"gemini ignores non-text parts", parseGeminiAnswer, `{"candidates":[{"content":{"parts":[{"text":"first"},{"inlineData":{"data":"aA=="}},{"text":"second"}]}}]}`, "first\nsecond"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.parse([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("text = %q, want %q", got, test.want)
			}
		})
	}
}

func TestImageMagicTypes(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		mime string
	}{
		{"png", testPNG, "image/png"},
		{"jpeg", testJPEG, "image/jpeg"},
		{"gif87a", []byte("GIF87a payload"), "image/gif"},
		{"gif89a", []byte("GIF89a payload"), "image/gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPpayload"), "image/webp"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image.dat")
			writeTestImage(t, path, test.data)
			images, err := loadImages(context.Background(), []string{path})
			if err != nil {
				t.Fatal(err)
			}
			if len(images) != 1 || images[0].mimeType != test.mime || images[0].data != base64.StdEncoding.EncodeToString(test.data) {
				t.Fatalf("unexpected image: %#v", images)
			}
		})
	}
}

func TestImageValidationFailsBeforeHTTP(t *testing.T) {
	tests := []struct {
		name  string
		paths func(*testing.T, string) []string
		want  string
	}{
		{"none", func(*testing.T, string) []string { return nil }, "at least one"},
		{"five", func(t *testing.T, dir string) []string {
			path := filepath.Join(dir, "image.png")
			writeTestImage(t, path, testPNG)
			return []string{path, path, path, path, path}
		}, "at most 4"},
		{"relative", func(*testing.T, string) []string { return []string{"image.png"} }, "absolute"},
		{"directory", func(_ *testing.T, dir string) []string { return []string{dir} }, "regular file"},
		{"missing", func(_ *testing.T, dir string) []string { return []string{filepath.Join(dir, "missing.png")} }, "stat image"},
		{"bad magic", func(t *testing.T, dir string) []string {
			path := filepath.Join(dir, "fake.png")
			writeTestImage(t, path, []byte("not an image"))
			return []string{path}
		}, "magic bytes"},
		{"riff wave", func(t *testing.T, dir string) []string {
			path := filepath.Join(dir, "fake.webp")
			writeTestImage(t, path, []byte("RIFF\x00\x00\x00\x00WAVEpayload"))
			return []string{path}
		}, "magic bytes"},
		{"total budget", func(t *testing.T, dir string) []string {
			first := filepath.Join(dir, "first.png")
			second := filepath.Join(dir, "second.png")
			writeSizedTestImage(t, first, testPNG, 10<<20)
			writeSizedTestImage(t, second, testPNG, (10<<20)+1)
			return []string{first, second}
		}, "20 MiB total budget"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{}`)
			}))
			defer server.Close()
			_, err := Ask(context.Background(), Provider{
				API: "openai-completions", BaseURL: server.URL, Model: "vision-model",
			}, test.paths(t, t.TempDir()), "q")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if calls.Load() != 0 {
				t.Fatalf("provider received %d requests", calls.Load())
			}
		})
	}
}

func TestExactTotalImageBudget(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.png")
	second := filepath.Join(dir, "second.png")
	writeSizedTestImage(t, first, testPNG, 10<<20)
	writeSizedTestImage(t, second, testPNG, 10<<20)
	images, err := loadImages(context.Background(), []string{first, second})
	if err != nil {
		t.Fatalf("exact 20 MiB rejected: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("got %d images", len(images))
	}
}

func writeTestImage(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSizedTestImage(t *testing.T, path string, magic []byte, size int64) {
	t.Helper()
	writeTestImage(t, path, magic)
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func jsonValue(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("invalid JSON %q: %v", data, err)
	}
	return value
}
