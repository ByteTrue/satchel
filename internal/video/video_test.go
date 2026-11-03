package video

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testPNG = mustBase64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	testMP4 = []byte("\x00\x00\x00\x18ftypmp42satchel-video-fixture")
)

func mustBase64(value string) []byte {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return data
}

func publicTestURL(t *testing.T, serverURL, path string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = net.JoinHostPort("public.example", u.Port())
	u.Path = path
	return u.String()
}

func fakePublicDownloader(server *httptest.Server) *videoDownloader {
	address := server.Listener.Addr().String()
	return &videoDownloader{
		lookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dialPinned: func(ctx context.Context, _ *net.Dialer, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
}

func videoOutputPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "result.mp4")
}

func TestGenerateOpenAIVideosCreatesPollsAndWritesContent(t *testing.T) {
	const apiKey = "openai-video-secret"
	const customSecret = "openai-custom-secret"
	firstPath := filepath.Join(t.TempDir(), "first.png")
	if err := os.WriteFile(firstPath, testPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	reference := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG)
	var created atomic.Bool
	var polled atomic.Bool
	var contentServed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey || r.Header.Get("X-Custom") != customSecret {
			t.Errorf("provider credentials missing: Authorization=%q X-Custom=%q", r.Header.Get("Authorization"), r.Header.Get("X-Custom"))
		}
		switch r.URL.Path {
		case "/v1/videos":
			created.Store(true)
			if r.Method != http.MethodPost {
				t.Errorf("create method = %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create: %v", err)
			}
			if body["model"] != "sora-test" || body["prompt"] != "a kite sways" || body["seconds"] != "6" || body["size"] != "1280x720" {
				t.Errorf("create body = %#v", body)
			}
			if value, _ := body["input_reference"].(string); !strings.HasPrefix(value, "data:image/png;base64,") {
				t.Errorf("input_reference = %#v", body["input_reference"])
			}
			if refs, _ := body["reference_images"].([]any); len(refs) != 1 || refs[0] != reference {
				t.Errorf("reference_images = %#v", body["reference_images"])
			}
			_, _ = w.Write([]byte(`{"id":"video-1","status":"queued"}`))
		case "/v1/videos/video-1":
			polled.Store(true)
			if r.Method != http.MethodGet {
				t.Errorf("poll method = %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"id":"video-1","status":"completed"}`))
		case "/v1/videos/video-1/content":
			contentServed.Store(true)
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(testMP4)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output := videoOutputPath(t)
	result, err := Generate(context.Background(), Provider{
		API: "openai-videos", BaseURL: server.URL + "/v1", APIKey: apiKey, Model: "sora-test",
		Headers: map[string]string{"X-Custom": customSecret}, HTTPClient: server.Client(),
	}, Request{
		Prompt: "a kite sways", Seconds: 6, Size: "1280x720", Image: firstPath, ReferenceImages: []string{reference},
	}, output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != output {
		t.Fatalf("result path = %q, want %q", result.Path, output)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != string(testMP4) {
		t.Fatalf("output = %d bytes, err = %v", len(data), err)
	}
	if !created.Load() || !polled.Load() || !contentServed.Load() {
		t.Fatalf("created=%v polled=%v content=%v", created.Load(), polled.Load(), contentServed.Load())
	}
}

func TestGenerateArkMapsRequestPollsAndDownloadsWithoutCredentials(t *testing.T) {
	const apiKey = "ark-video-secret"
	const customSecret = "ark-custom-secret"
	const signature = "signed-video-token"
	var downloadCalled atomic.Bool
	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadCalled.Store(true)
		if r.URL.Query().Get("signature") != signature {
			t.Errorf("signed query was not preserved: %q", r.URL.RawQuery)
		}
		for _, key := range []string{"Authorization", "X-Custom", "Cookie", "Referer"} {
			if got := r.Header.Get(key); got != "" {
				t.Errorf("download received %s = %q", key, got)
			}
		}
		_, _ = w.Write(testMP4)
	}))
	defer downloadServer.Close()
	videoURL := publicTestURL(t, downloadServer.URL, "/result.mp4") + "?signature=" + signature
	var polls atomic.Int32
	var createBody map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/contents/generations/tasks":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Errorf("decode Ark create: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"ark-task"}`))
		case "/api/v3/contents/generations/tasks/ark-task":
			if polls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"id":"ark-task","status":"running"}`))
				return
			}
			fmt.Fprintf(w, `{"id":"ark-task","status":"succeeded","content":{"video_url":%q}}`, videoURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	image := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG)
	output := videoOutputPath(t)
	result, err := generate(context.Background(), Provider{
		API: "ark", BaseURL: provider.URL + "/api/v3", APIKey: apiKey, Model: "seedance-test",
		Headers: map[string]string{"X-Custom": customSecret}, HTTPClient: provider.Client(),
	}, Request{Prompt: "a bird", Seconds: 5, Size: "1280x720", Image: image, ReferenceImages: []string{image}}, output, fakePublicDownloader(downloadServer), time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != output || polls.Load() != 2 || !downloadCalled.Load() {
		t.Fatalf("result=%#v polls=%d download=%v", result, polls.Load(), downloadCalled.Load())
	}
	if createBody["duration"] != float64(5) || createBody["ratio"] != "16:9" || createBody["resolution"] != "720p" {
		t.Errorf("Ark mapping = %#v", createBody)
	}
	content, _ := createBody["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("Ark content = %#v", createBody["content"])
	}
	first, _ := content[1].(map[string]any)
	reference, _ := content[2].(map[string]any)
	if first["role"] != "first_frame" || reference["role"] != "reference_image" {
		t.Errorf("Ark image roles = %#v, %#v", first, reference)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != string(testMP4) {
		t.Fatalf("Ark output = %d bytes, err = %v", len(data), err)
	}
}

func TestGenerateFailureRedactsSecretsAndRemovesOutput(t *testing.T) {
	const apiKey = "provider-failure-secret"
	const customSecret = "header-failure-secret"
	const signature = "signed-failure-token"
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"id":"failed-task"}`))
			return
		}
		fmt.Fprintf(w, `{"status":"failed","error":{"code":"bad_prompt","message":%q}}`,
			"rejected "+apiKey+" and "+customSecret+" at https://cdn.example.test/v.mp4?signature="+signature)
	}))
	defer provider.Close()
	output := videoOutputPath(t)
	_, err := generate(context.Background(), Provider{
		API: "ark", BaseURL: provider.URL, APIKey: apiKey, Model: "m",
		Headers: map[string]string{"X-Custom": customSecret}, HTTPClient: provider.Client(),
	}, Request{Prompt: "x"}, output, nil, time.Millisecond)
	if err == nil {
		t.Fatal("failed generation succeeded")
	}
	for _, secret := range []string{apiKey, customSecret, signature, "cdn.example.test"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
	if !strings.Contains(err.Error(), "[URL]") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error was not visibly sanitized: %v", err)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed output still exists: %v", statErr)
	}
}

func TestGenerateCancellationDuringPollingRemovesOutput(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"slow-task"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer provider.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	polled := make(chan struct{})
	go func() {
		<-polled
		cancel()
	}()
	// The client is wrapped only to observe the first completed poll.
	client := provider.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(req)
		if req.Method == http.MethodGet {
			once.Do(func() { close(polled) })
		}
		return res, err
	})
	output := videoOutputPath(t)
	_, err := generate(ctx, Provider{API: "ark", BaseURL: provider.URL, Model: "m", HTTPClient: client}, Request{Prompt: "x"}, output, nil, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cancelled output still exists: %v", statErr)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGenerateValidatesPathsAndNeverOverwrites(t *testing.T) {
	var called atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		_, _ = w.Write([]byte(`{"id":"unexpected"}`))
	}))
	defer provider.Close()
	configured := Provider{API: "ark", BaseURL: provider.URL, Model: "m", HTTPClient: provider.Client()}
	for _, test := range []struct {
		name    string
		request Request
		path    string
		want    string
	}{
		{name: "relative output", request: Request{Prompt: "x"}, path: "result.mp4", want: "absolute"},
		{name: "seconds", request: Request{Prompt: "x", Seconds: 16}, path: videoOutputPath(t), want: "1 to 15"},
		{name: "Ark size", request: Request{Prompt: "x", Size: "junk"}, path: videoOutputPath(t), want: "WIDTHxHEIGHT"},
		{name: "relative reference", request: Request{Prompt: "x", Image: "first.png"}, path: videoOutputPath(t), want: "absolute"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Generate(context.Background(), configured, test.request, test.path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	output := videoOutputPath(t)
	if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Generate(context.Background(), configured, Request{Prompt: "x"}, output)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing output error = %v", err)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("existing output = %q, %v", data, readErr)
	}
	if called.Load() {
		t.Fatal("invalid paths or an existing output reached the provider")
	}
}
